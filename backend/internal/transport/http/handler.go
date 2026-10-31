package http

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
	"stock-api/internal/domain"
	"stock-api/internal/platform/auth"
	"stock-api/internal/reservation"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// Handler contiene las dependencias.
type Handler struct {
	repo         domain.EventRepository
	rdb          *redis.Client
	reservations *reservation.Service
}

// NewHandler crea el manejador inyectando Repo, Redis y el servicio de reservas.
func NewHandler(repo domain.EventRepository, rdb *redis.Client, reservations *reservation.Service) *Handler {
	return &Handler{
		repo:         repo,
		rdb:          rdb,
		reservations: reservations,
	}
}

// isFinalStatus indica si el estado ya no va a cambiar (el SSE puede cerrarse).
func isFinalStatus(status string) bool {
	switch status {
	case domain.OrderConfirmed, domain.OrderRejected, domain.OrderExpired:
		return true
	}
	return false
}

// expiresIn devuelve los segundos que le quedan a la reserva de la orden.
func (h *Handler) expiresIn(c *gin.Context, orderID int64) int {
	timeLeft, err := h.reservations.TimeLeft(c.Request.Context(), orderID)
	if err != nil {
		return 0
	}
	return int(timeLeft.Seconds())
}

// GetEvent maneja la ruta GET /events/:id
func (h *Handler) GetEvent(c *gin.Context) {
	idParam := c.Param("id")
	id, err := strconv.ParseInt(idParam, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	event, err := h.repo.GetByID(c.Request.Context(), id)
	if err != nil {
		if err == domain.ErrEventNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "event not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	c.JSON(http.StatusOK, event)
}

// InternalOrder define lo que enviamos a Redis para que el Worker lo lea.
// Los ítems no viajan en la cola: el worker los lee de orden_items.
type InternalOrder struct {
	OrderID int64 `json:"order_id"` // <--- Importante: El ID de la orden para seguimiento
	EventID int64 `json:"event_id"`
	UserID  int64 `json:"user_id"`
}

type comprarItem struct {
	SectorID int64 `json:"sector_id"`
	Cantidad int   `json:"cantidad"`
}

type comprarRequest struct {
	Items []comprarItem `json:"items"`
}

// purchaseError ubica el problema del carrito: Index es la línea de la request (-1 = el carrito entero).
type purchaseError struct {
	Index int
	Err   error
}

// validatePurchase controla el carrito contra el evento y lo normaliza: une las líneas
// repetidas de un mismo sector y las ordena por sector_id (el orden en que el worker
// bloquea los sectores). Cada sector tiene que ser del evento, cada cantidad >= 1 y el total
// no puede superar el límite por orden. El stock lo vuelve a controlar el worker de forma
// atómica; acá solo cortamos antes de encolar lo que ya sabemos agotado.
func validatePurchase(event *domain.Event, items []comprarItem) ([]domain.OrderItemInput, *purchaseError) {
	if len(items) == 0 {
		return nil, &purchaseError{-1, domain.ErrOverPurchaseLimit}
	}
	porSector := map[int64]int{}
	primeraLinea := map[int64]int{}
	total := 0
	for i, item := range items {
		if _, ok := event.Sector(item.SectorID); !ok {
			return nil, &purchaseError{i, domain.ErrSectorNotFound}
		}
		if item.Cantidad < 1 || item.Cantidad > event.LimiteTicketsPorOrden {
			return nil, &purchaseError{i, domain.ErrOverPurchaseLimit}
		}
		if _, ok := primeraLinea[item.SectorID]; !ok {
			primeraLinea[item.SectorID] = i
		}
		porSector[item.SectorID] += item.Cantidad
		total += item.Cantidad
	}
	if total > event.LimiteTicketsPorOrden {
		return nil, &purchaseError{-1, domain.ErrOverPurchaseLimit}
	}

	normalized := make([]domain.OrderItemInput, 0, len(porSector))
	for sectorID, cantidad := range porSector {
		normalized = append(normalized, domain.OrderItemInput{SectorID: sectorID, Cantidad: cantidad})
	}
	sort.Slice(normalized, func(a, b int) bool { return normalized[a].SectorID < normalized[b].SectorID })
	for _, item := range normalized {
		if sector, _ := event.Sector(item.SectorID); sector.StockDisponible < item.Cantidad {
			return nil, &purchaseError{primeraLinea[item.SectorID], domain.ErrNotEnoughStock}
		}
	}
	return normalized, nil
}

func itemField(i int, name string) string {
	if i < 0 {
		return "items"
	}
	return fmt.Sprintf("items.%d.%s", i, name)
}

// ComprarTickets: POST /events/:id/comprar {items: [{sector_id, cantidad}, ...]}
// Valida el carrito -> Crea orden en BD (Pendiente) -> Envía a Cola -> Responde al usuario
func (h *Handler) ComprarTickets(c *gin.Context) {
	// 1. Validar ID del evento
	idParam := c.Param("id")
	eventID, err := strconv.ParseInt(idParam, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	// 2. Obtener la identidad verificada por AuthMiddleware.
	userID := c.GetInt64(auth.UserIDContextKey)
	if userID <= 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}

	var req comprarRequest
	if err := c.ShouldBindJSON(&req); err != nil || len(req.Items) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Agregá al menos una entrada al carrito", "field": "items"})
		return
	}

	event, err := h.repo.GetByID(c.Request.Context(), eventID)
	if err != nil {
		if errors.Is(err, domain.ErrEventNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "event not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	items, perr := validatePurchase(event, req.Items)
	if perr != nil {
		switch {
		case errors.Is(perr.Err, domain.ErrSectorNotFound):
			c.JSON(http.StatusBadRequest, gin.H{"error": "El sector no pertenece a este evento", "field": itemField(perr.Index, "sector_id")})
		case errors.Is(perr.Err, domain.ErrOverPurchaseLimit):
			c.JSON(http.StatusBadRequest, gin.H{
				"error":  "Podés comprar entre 1 y " + strconv.Itoa(event.LimiteTicketsPorOrden) + " entradas por orden, sumando todos los sectores",
				"field":  itemField(perr.Index, "cantidad"),
				"limite": event.LimiteTicketsPorOrden,
			})
		default: // ErrNotEnoughStock
			sector, _ := event.Sector(req.Items[perr.Index].SectorID)
			c.JSON(http.StatusConflict, gin.H{
				"error": "No quedan entradas suficientes en " + sector.Nombre,
				"field": itemField(perr.Index, "cantidad"),
			})
		}
		return
	}

	// 3. --- REGISTRAR ORDEN EN MYSQL ---
	orderID, err := h.repo.CreateOrder(c.Request.Context(), eventID, userID, items)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "error creating order in database"})
		return
	}

	// 4. --- EMPAQUETAR PARA REDIS ---
	orderPayload := InternalOrder{
		OrderID: orderID,
		EventID: eventID,
		UserID:  userID,
	}

	jsonBody, err := json.Marshal(orderPayload)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "error packing order"})
		return
	}

	// 5. Empujar a la cola "purchase_queue"
	// RECORDATORIO: RPush para FIFO (El primero que llega, gana)
	err = h.rdb.RPush(c.Request.Context(), "purchase_queue", jsonBody).Err()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "error enqueuing order"})
		return
	}

	// 6. Responder al usuario
	c.JSON(http.StatusOK, gin.H{
		"message":  "Solicitud recibida. Tu compra está siendo procesada.",
		"order_id": orderID,
		"status":   "procesando",
	})
}

type orderItemView struct {
	SectorID       int64   `json:"sector_id"`
	Sector         string  `json:"sector"`
	Cantidad       int     `json:"cantidad"`
	PrecioUnitario float64 `json:"precio_unitario"`
}

// CheckStatus: GET /orders/:id
// Devuelve el estado y el detalle completo de la orden del usuario: es lo que usa el
// frontend para retomar una compra al volver de Mercado Pago (o tras recargar la página).
// Las entradas nominativas, con su QR, vienen solo cuando la orden está confirmada.
func (h *Handler) CheckStatus(c *gin.Context) {
	orderID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ID inválido"})
		return
	}
	ctx := c.Request.Context()

	order, err := h.repo.GetOrderDetails(ctx, orderID)
	if err != nil || order.UserID != c.GetInt64(auth.UserIDContextKey) {
		if err != nil && !errors.Is(err, domain.ErrOrderNotFound) {
			log.Printf("Error buscando la orden #%d: %v", orderID, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "No se pudo cargar la orden"})
			return
		}
		// Una orden de otro usuario se reporta igual que una inexistente.
		c.JSON(http.StatusNotFound, gin.H{"error": "Orden no encontrada"})
		return
	}

	mensaje := ""
	position := 0 // 0 significa que no aplica (ya reservada, comprada o rechazada)
	expiresIn := 0
	switch order.Status {
	case domain.OrderPending:
		position, _ = h.repo.GetQueuePosition(ctx, orderID, order.EventID)
		mensaje = "Estás en la fila virtual."
	case domain.OrderReserved:
		expiresIn = h.expiresIn(c, orderID)
		mensaje = "🕒 Entradas reservadas. Completá el pago antes de que expire la reserva."
	case domain.OrderConfirmed:
		mensaje = "🎟️ ¡Felicidades! Tenés tus entradas."
	case domain.OrderRejected:
		mensaje = "⛔ Lo sentimos, se agotó el stock o el pago fue rechazado."
	case domain.OrderExpired:
		mensaje = "⌛ Tu reserva expiró y las entradas fueron liberadas."
	}

	items := make([]orderItemView, len(order.Items))
	for i, item := range order.Items {
		items[i] = orderItemView{SectorID: item.SectorID, Sector: item.SectorName, Cantidad: item.Quantity, PrecioUnitario: item.UnitPrice}
	}
	tickets := []ticketView{}
	if order.Status == domain.OrderConfirmed {
		all, err := h.repo.GetOrderTickets(ctx, orderID)
		if err == nil {
			tickets, err = ticketViews(all)
		}
		if err != nil {
			log.Printf("Error cargando las entradas de la orden #%d: %v", orderID, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "No se pudieron cargar las entradas"})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"order_id":   orderID,
		"status":     order.Status,
		"mensaje":    mensaje,
		"position":   position,
		"expires_in": expiresIn,
		"evento":     gin.H{"id": order.EventID, "nombre": order.EventName},
		"items":      items,
		"total":      order.Total(),
		"comprador":  gin.H{"nombre": order.BuyerName, "dni": order.BuyerDNI},
		"tickets":    tickets,
	})
}

// StreamOrderStatus transmite los cambios de estado de la orden usando Redis Pub/Sub.
// 'reservada' se envía y el stream sigue abierto esperando el pago; los estados
// finales (confirmada / rechazada / expirada) se envían y cierran el stream.
func (h *Handler) StreamOrderStatus(c *gin.Context) {
	idParam := c.Param("id")
	orderID, err := strconv.ParseInt(idParam, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ID inválido"})
		return
	}

	ctx := c.Request.Context()
	pubsub := h.rdb.Subscribe(ctx, "order_status_"+idParam)
	defer pubsub.Close()

	if _, err := pubsub.Receive(ctx); err != nil {
		if ctx.Err() == nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "error subscribing to order status"})
		}
		return
	}

	orderStatus, err := h.repo.GetOrderStatus(ctx, orderID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Orden no encontrada"})
		return
	}
	if orderStatus.UserID != c.GetInt64(auth.UserIDContextKey) {
		c.JSON(http.StatusNotFound, gin.H{"error": "Orden no encontrada"})
		return
	}
	status := orderStatus.Status

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)

	initial := reservation.StatusUpdate{Status: status, QRToken: orderStatus.QRToken}
	if status == domain.OrderReserved {
		initial.ExpiresIn = h.expiresIn(c, orderID)
	}
	if status != domain.OrderPending {
		c.SSEvent("message", initial)
		c.Writer.Flush()
	}
	if isFinalStatus(status) {
		return
	}

	// Mientras el usuario paga en Mercado Pago el stream puede quedar abierto varios
	// minutos: mandamos un comentario SSE periódico para que proxies no lo corten.
	keepAlive := time.NewTicker(25 * time.Second)
	defer keepAlive.Stop()

	messages := pubsub.Channel()
	for {
		select {
		case <-ctx.Done():
			return
		case <-keepAlive.C:
			if _, err := c.Writer.WriteString(": keepalive\n\n"); err != nil {
				return
			}
			c.Writer.Flush()
		case msg, ok := <-messages:
			if !ok {
				return
			}
			var update reservation.StatusUpdate
			if err := json.Unmarshal([]byte(msg.Payload), &update); err != nil {
				continue
			}
			if update.Status != domain.OrderReserved && !isFinalStatus(update.Status) {
				continue
			}
			c.SSEvent("message", update)
			c.Writer.Flush()
			if isFinalStatus(update.Status) {
				return
			}
		}
	}
}

// GetEvents: GET /events (Lista todos los eventos para la Home)
func (h *Handler) GetEvents(c *gin.Context) {
	eventos, err := h.repo.GetAllEvents(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "error fetching events"})
		return
	}
	c.JSON(http.StatusOK, eventos)
}
