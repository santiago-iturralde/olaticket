package http

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html/template"
	"log"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"stock-api/internal/domain"
	"stock-api/internal/platform/auth"
	"stock-api/internal/platform/email"
	"stock-api/internal/platform/payment"
	"stock-api/internal/platform/pdf"
	"stock-api/internal/reservation"
)

type PaymentHandler struct {
	repo         domain.EventRepository
	reservations *reservation.Service
	gateway      payment.Gateway
	mailer       *email.Sender
	frontendURL  string
}

func NewPaymentHandler(
	repo domain.EventRepository,
	reservations *reservation.Service,
	gateway payment.Gateway,
	mailer *email.Sender,
	frontendURL string,
) *PaymentHandler {
	return &PaymentHandler{repo: repo, reservations: reservations, gateway: gateway, mailer: mailer, frontendURL: frontendURL}
}

type checkoutRequest struct {
	Asistentes []asistenteRequest `json:"asistentes"`
}

// Checkout: POST /orders/:id/checkout (JWT) {asistentes: [{sector_id, nombre, dni}, ...]}
// Guarda quién usa cada entrada (una persona por entrada, se vuelven nominativas al pagar),
// genera la preferencia de pago para la orden reservada del usuario y devuelve el init_point.
func (h *PaymentHandler) Checkout(c *gin.Context) {
	orderID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ID inválido"})
		return
	}
	ctx := c.Request.Context()

	var req checkoutRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Cargá los datos de cada asistente", "field": "asistentes"})
		return
	}

	order, err := h.repo.GetOrderDetails(ctx, orderID)
	if err != nil {
		if errors.Is(err, domain.ErrOrderNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Orden no encontrada"})
			return
		}
		log.Printf("Error buscando orden #%d para checkout: %v", orderID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "error iniciando el pago"})
		return
	}
	if order.UserID != c.GetInt64(auth.UserIDContextKey) {
		c.JSON(http.StatusNotFound, gin.H{"error": "Orden no encontrada"})
		return
	}
	if order.Status != domain.OrderReserved {
		c.JSON(http.StatusConflict, gin.H{"error": "La orden no está reservada", "status": order.Status})
		return
	}

	timeLeft, err := h.reservations.TimeLeft(ctx, orderID)
	if err != nil {
		log.Printf("Error leyendo TTL de la orden #%d: %v", orderID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "error iniciando el pago"})
		return
	}
	if timeLeft <= 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "La reserva expiró", "status": domain.OrderExpired})
		return
	}

	asistentes, ferr := validateAttendees(order, req.Asistentes)
	if ferr != nil {
		ferr.respond(c)
		return
	}
	saved, err := h.repo.SetAttendees(ctx, orderID, asistentes)
	if err != nil {
		log.Printf("Error guardando asistentes de la orden #%d: %v", orderID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "error iniciando el pago"})
		return
	}
	if !saved { // Expiró o se pagó entre la lectura y el guardado
		c.JSON(http.StatusConflict, gin.H{"error": "La orden ya no está reservada"})
		return
	}

	items := make([]payment.PreferenceItem, len(order.Items))
	for i, item := range order.Items {
		items[i] = payment.PreferenceItem{
			ID:        fmt.Sprintf("evento-%d-sector-%d", order.EventID, item.SectorID),
			Title:     item.Title(order.EventName),
			Quantity:  item.Quantity,
			UnitPrice: item.UnitPrice,
		}
	}
	pref, err := h.gateway.CreatePreference(ctx, payment.PreferenceRequest{
		OrderID:   order.ID,
		EventID:   order.EventID,
		Items:     items,
		ExpiresAt: time.Now().Add(timeLeft),
	})
	if err != nil {
		log.Printf("Error creando preferencia para la orden #%d: %v", orderID, err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "No se pudo conectar con Mercado Pago"})
		return
	}

	mode := "mercadopago"
	if h.gateway.IsMock() {
		mode = "sandbox"
	}
	c.JSON(http.StatusOK, gin.H{
		"order_id":      order.ID,
		"preference_id": pref.ID,
		"init_point":    pref.InitPoint,
		"amount":        order.Total(),
		"expires_in":    int(timeLeft.Seconds()),
		"mode":          mode,
	})
}

type webhookBody struct {
	Type string `json:"type"`
	Data struct {
		ID string `json:"id"`
	} `json:"data"`
}

// Webhook: POST /payments/webhook (público, lo consume Mercado Pago).
// Acepta el formato Webhooks (?type=payment&data.id=X) y el IPN legado (?topic=payment&id=X).
// Respondemos 2xx solo cuando procesamos (o descartamos) la notificación; ante errores
// transitorios devolvemos 5xx para que MP reintente.
func (h *PaymentHandler) Webhook(c *gin.Context) {
	topic := c.Query("type")
	if topic == "" {
		topic = c.Query("topic")
	}
	paymentID := c.Query("data.id")
	if paymentID == "" {
		paymentID = c.Query("id")
	}

	var body webhookBody
	if err := c.ShouldBindJSON(&body); err == nil {
		if topic == "" {
			topic = body.Type
		}
		if paymentID == "" {
			paymentID = body.Data.ID
		}
	}

	if topic != "payment" || paymentID == "" {
		c.Status(http.StatusOK)
		return
	}
	if !h.gateway.VerifyWebhook(c.Request, c.Query("data.id")) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "firma inválida"})
		return
	}

	if err := h.processPayment(c.Request.Context(), paymentID); err != nil {
		log.Printf("Error procesando pago %s: %v", paymentID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "error procesando el pago"})
		return
	}
	c.Status(http.StatusOK)
}

func (h *PaymentHandler) processPayment(ctx context.Context, paymentID string) error {
	p, err := h.gateway.GetPayment(ctx, paymentID)
	if err != nil {
		if errors.Is(err, payment.ErrPaymentNotFound) {
			log.Printf("Webhook con pago desconocido %s: ignorado", paymentID)
			return nil
		}
		return err
	}

	order, err := h.repo.GetOrderDetails(ctx, p.OrderID)
	if err != nil {
		if errors.Is(err, domain.ErrOrderNotFound) {
			log.Printf("Pago %s referencia la orden inexistente #%d: ignorado", p.ID, p.OrderID)
			return nil
		}
		return err
	}

	switch p.Status {
	case payment.StatusApproved:
		if !h.gateway.IsMock() && math.Abs(p.Amount-order.Total()) > 0.01 {
			log.Printf("⚠️  Pago %s de $%.2f no coincide con el total $%.2f de la orden #%d: no se confirma",
				p.ID, p.Amount, order.Total(), order.ID)
			return nil
		}
		qrToken, err := h.reservations.Confirm(ctx, order, p.ID)
		if err != nil {
			return err
		}
		if qrToken != "" {
			log.Printf("💳 Orden #%d CONFIRMADA (pago %s)", order.ID, p.ID)
			// La orden ya quedó confirmada: el envío de la entrada no bloquea ni puede revertir el webhook.
			go h.sendTicketEmail(*order)
		} else if order.Status != domain.OrderConfirmed {
			log.Printf("⚠️  Pago %s aprobado pero la orden #%d está '%s': requiere reembolso manual",
				p.ID, order.ID, order.Status)
		}

	case payment.StatusRejected, payment.StatusCancelled:
		released, err := h.reservations.Release(ctx, order.ID, domain.OrderRejected, "El pago fue rechazado o cancelado.")
		if err != nil {
			return err
		}
		if released {
			log.Printf("⛔ Orden #%d RECHAZADA por pago %s (%s): stock devuelto", order.ID, p.ID, p.Status)
		}

	default:
		// pending / in_process / authorized: esperamos la próxima notificación.
		log.Printf("Pago %s de la orden #%d en estado '%s'", p.ID, order.ID, p.Status)
	}
	return nil
}

const ticketEmailHTML = `<!doctype html>
<html lang="es"><body style="margin:0;padding:24px;background:#f5f5f5;font-family:sans-serif;color:#222">
<div style="max-width:480px;margin:0 auto;background:#fff;border-radius:12px;padding:32px">
<h2 style="margin-top:0;color:#009ee3">¡Tu pago fue procesado con éxito! Adjuntamos tus entradas oficiales.</h2>
<p><strong>{{.EventName}}</strong><br>Orden #{{.OrderID}} · {{.Quantity}} entrada(s) · $ {{printf "%.2f" .Total}}</p>
<table style="width:100%;border-collapse:collapse;font-size:14px">
{{range .Tickets}}<tr><td style="padding:4px 0">{{.Nombre}} · DNI {{.DNI}}</td><td style="padding:4px 0;text-align:right">{{if .SectorName}}{{.SectorName}}{{else}}Entrada{{end}}</td></tr>
{{end}}</table>
<p style="color:#666;font-size:13px">El PDF adjunto tiene una página por persona, cada una con su propio código QR. Cada asistente presenta el suyo y su DNI en el ingreso.</p>
</div></body></html>`

var ticketEmailTemplate = template.Must(template.New("ticket-email").Parse(ticketEmailHTML))

// sendTicketEmail genera el PDF con una página por entrada nominativa y lo envía al comprador.
// Corre en su propia goroutine después de confirmar: cualquier fallo se registra en el log,
// pero la orden sigue 'confirmada' porque el pago ya ingresó (las entradas siguen en la web).
func (h *PaymentHandler) sendTicketEmail(order domain.Order) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("❌ Panic enviando la entrada de la orden #%d: %v", order.ID, r)
		}
	}()
	if !h.mailer.Enabled() {
		log.Printf("⚠️  SMTP no configurado: no se envía la entrada de la orden #%d por email", order.ID)
		return
	}
	if order.UserEmail == "" {
		log.Printf("❌ Orden #%d sin email de usuario: no se envía la entrada", order.ID)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tickets, err := h.repo.GetOrderTickets(ctx, order.ID)
	if err != nil {
		log.Printf("❌ Error leyendo las entradas de la orden #%d: %v", order.ID, err)
		return
	}
	pdfTickets := make([]pdf.Ticket, len(tickets))
	for i, t := range tickets {
		code, err := auth.GenerateEntryCode(t.ID)
		if err != nil {
			log.Printf("❌ Error firmando la entrada #%d: %v", t.ID, err)
			return
		}
		pdfTickets[i] = pdf.Ticket{ID: t.ID, SectorName: t.SectorName, Nombre: t.Nombre, DNI: t.DNI, Code: code}
	}
	ticketPDF, err := pdf.GenerateTickets(pdf.OrderTickets{
		OrderID:   order.ID,
		EventName: order.EventName,
		Tickets:   pdfTickets,
		IssuedAt:  time.Now(),
	})
	if err != nil {
		log.Printf("❌ Error generando el PDF de la orden #%d: %v", order.ID, err)
		return
	}

	var body bytes.Buffer
	if err := ticketEmailTemplate.Execute(&body, gin.H{
		"EventName": order.EventName,
		"Tickets":   tickets,
		"OrderID":   order.ID,
		"Quantity":  order.Quantity(),
		"Total":     order.Total(),
	}); err != nil {
		log.Printf("❌ Error armando el email de la orden #%d: %v", order.ID, err)
		return
	}

	err = h.mailer.Send(email.Message{
		To:      order.UserEmail,
		Subject: "Tus entradas para " + order.EventName,
		HTML:    body.String(),
		Attachments: []email.Attachment{{
			Filename:    fmt.Sprintf("olaticket-orden-%d.pdf", order.ID),
			ContentType: "application/pdf",
			Data:        ticketPDF,
		}},
	})
	if err != nil {
		log.Printf("❌ Error enviando la entrada de la orden #%d: %v", order.ID, err)
		return
	}
	log.Printf("📧 Entrada de la orden #%d enviada a %s", order.ID, order.UserEmail)
}

var mockCheckoutPage = template.Must(template.New("mock").Parse(`<!doctype html>
<html lang="es"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Mercado Pago (SANDBOX)</title>
<style>
body{margin:0;min-height:100vh;display:flex;align-items:center;justify-content:center;background:#f5f5f5;font-family:sans-serif;color:#222}
.card{background:#fff;padding:32px;border-radius:12px;max-width:380px;width:100%;box-shadow:0 4px 20px rgba(0,0,0,.08);text-align:center}
.tag{display:inline-block;background:#fff3cd;color:#8a6d00;padding:4px 10px;border-radius:4px;font-size:12px;font-weight:bold}
.amount{font-size:2rem;font-weight:bold;margin:12px 0}
button{width:100%;padding:14px;margin-top:10px;border:0;border-radius:6px;font-size:1rem;font-weight:bold;cursor:pointer}
.ok{background:#009ee3;color:#fff}.ko{background:#eee;color:#b00020}
a{color:#009ee3}
</style></head><body><div class="card">
<span class="tag">SANDBOX / MOCK</span>
{{if .Result}}
  <h2>Pago {{if eq .Result "approved"}}aprobado ✅{{else}}rechazado ❌{{end}}</h2>
  <p>Podés cerrar esta ventana o <a href="{{.BackURL}}">volver a Olaticket</a>.</p>
{{else}}
  <h2>{{.EventName}}</h2>
  <p>Orden #{{.OrderID}} · {{.Quantity}} entrada(s)</p>
  {{range .Items}}<p>{{.Quantity}} × {{if .SectorName}}{{.SectorName}}{{else}}Entrada{{end}} · $ {{printf "%.2f" .Subtotal}}</p>{{end}}
  <div class="amount">$ {{printf "%.2f" .Total}}</div>
  <form method="post">
    <input type="hidden" name="order_id" value="{{.OrderID}}">
    <button class="ok" name="result" value="approved">Aprobar pago</button>
    <button class="ko" name="result" value="rejected">Rechazar pago</button>
  </form>
{{end}}
</div></body></html>`))

// MockCheckoutPage: GET /payments/mock/checkout (solo en modo SANDBOX/MOCK).
func (h *PaymentHandler) MockCheckoutPage(c *gin.Context) {
	orderID, err := strconv.ParseInt(c.Query("order_id"), 10, 64)
	if err != nil {
		c.String(http.StatusBadRequest, "order_id inválido")
		return
	}
	order, err := h.repo.GetOrderDetails(c.Request.Context(), orderID)
	if err != nil {
		c.String(http.StatusNotFound, "Orden no encontrada")
		return
	}
	c.Status(http.StatusOK)
	c.Header("Content-Type", "text/html; charset=utf-8")
	_ = mockCheckoutPage.Execute(c.Writer, gin.H{
		"OrderID":   order.ID,
		"EventName": order.EventName,
		"Items":     order.Items,
		"Quantity":  order.Quantity(),
		"Total":     order.Total(),
	})
}

// MockCheckoutSubmit: POST /payments/mock/checkout. Simula la notificación de MP
// pasando por el mismo processPayment que usa el webhook real.
func (h *PaymentHandler) MockCheckoutSubmit(c *gin.Context) {
	orderID, err := strconv.ParseInt(c.PostForm("order_id"), 10, 64)
	if err != nil {
		c.String(http.StatusBadRequest, "order_id inválido")
		return
	}
	result := c.PostForm("result")
	if result != payment.StatusApproved {
		result = payment.StatusRejected
	}

	if err := h.processPayment(c.Request.Context(), payment.MockPaymentID(orderID, result)); err != nil {
		log.Printf("Error en pago simulado de la orden #%d: %v", orderID, err)
		c.String(http.StatusInternalServerError, "Error procesando el pago simulado")
		return
	}
	c.Status(http.StatusOK)
	c.Header("Content-Type", "text/html; charset=utf-8")
	_ = mockCheckoutPage.Execute(c.Writer, gin.H{
		"Result":  result,
		"BackURL": h.frontendURL + "/?order_id=" + strconv.FormatInt(orderID, 10),
	})
}
