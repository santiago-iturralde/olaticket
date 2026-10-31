package payment

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const mercadoPagoAPI = "https://api.mercadopago.com"

type MercadoPagoGateway struct {
	cfg    Config
	client *http.Client
}

func NewMercadoPagoGateway(cfg Config) *MercadoPagoGateway {
	return &MercadoPagoGateway{
		cfg:    cfg,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

func (g *MercadoPagoGateway) IsMock() bool { return false }

type mpItem struct {
	ID         string  `json:"id"`
	Title      string  `json:"title"`
	Quantity   int     `json:"quantity"`
	UnitPrice  float64 `json:"unit_price"`
	CurrencyID string  `json:"currency_id"`
}

type mpBackURLs struct {
	Success string `json:"success"`
	Failure string `json:"failure"`
	Pending string `json:"pending"`
}

type mpPreferenceRequest struct {
	Items             []mpItem   `json:"items"`
	ExternalReference string     `json:"external_reference"`
	NotificationURL   string     `json:"notification_url,omitempty"`
	BackURLs          mpBackURLs `json:"back_urls"`
	AutoReturn        string     `json:"auto_return,omitempty"`
	Expires           bool       `json:"expires"`
	ExpirationDateTo  string     `json:"expiration_date_to"`
	StatementDesc     string     `json:"statement_descriptor,omitempty"`
}

type mpPreferenceResponse struct {
	ID               string `json:"id"`
	InitPoint        string `json:"init_point"`
	SandboxInitPoint string `json:"sandbox_init_point"`
}

func (g *MercadoPagoGateway) CreatePreference(ctx context.Context, req PreferenceRequest) (*Preference, error) {
	backURL := fmt.Sprintf("%s/?order_id=%d", g.cfg.FrontendURL, req.OrderID)
	items := make([]mpItem, len(req.Items))
	for i, item := range req.Items {
		items[i] = mpItem{
			ID:         item.ID,
			Title:      item.Title,
			Quantity:   item.Quantity,
			UnitPrice:  item.UnitPrice,
			CurrencyID: "ARS",
		}
	}
	body := mpPreferenceRequest{
		Items:             items,
		ExternalReference: strconv.FormatInt(req.OrderID, 10),
		BackURLs:          mpBackURLs{Success: backURL, Failure: backURL, Pending: backURL},
		// La preferencia vence junto con la reserva: MP no deja pagar una orden ya expirada.
		Expires:          true,
		ExpirationDateTo: req.ExpiresAt.Format("2006-01-02T15:04:05.000-07:00"),
		StatementDesc:    "OLATICKET",
	}
	// MP solo acepta notification_url y auto_return con URLs públicas HTTPS.
	if strings.HasPrefix(g.cfg.PublicAPIURL, "https://") {
		body.NotificationURL = g.cfg.PublicAPIURL + "/payments/webhook"
	}
	if strings.HasPrefix(g.cfg.FrontendURL, "https://") {
		body.AutoReturn = "approved"
	}

	var res mpPreferenceResponse
	if err := g.do(ctx, http.MethodPost, "/checkout/preferences", body, &res); err != nil {
		return nil, fmt.Errorf("error creando preferencia: %w", err)
	}

	initPoint := res.InitPoint
	if strings.HasPrefix(g.cfg.AccessToken, "TEST-") && res.SandboxInitPoint != "" {
		initPoint = res.SandboxInitPoint
	}
	return &Preference{ID: res.ID, InitPoint: initPoint}, nil
}

type mpPaymentResponse struct {
	ID                int64   `json:"id"`
	Status            string  `json:"status"`
	ExternalReference string  `json:"external_reference"`
	TransactionAmount float64 `json:"transaction_amount"`
}

// GetPayment consulta el pago directamente a la API de MP: es la única fuente confiable.
func (g *MercadoPagoGateway) GetPayment(ctx context.Context, paymentID string) (*Payment, error) {
	if _, err := strconv.ParseInt(paymentID, 10, 64); err != nil {
		return nil, ErrPaymentNotFound
	}
	var res mpPaymentResponse
	if err := g.do(ctx, http.MethodGet, "/v1/payments/"+paymentID, nil, &res); err != nil {
		return nil, err
	}
	orderID, err := strconv.ParseInt(res.ExternalReference, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("pago %s sin external_reference válido", paymentID)
	}
	return &Payment{
		ID:      strconv.FormatInt(res.ID, 10),
		Status:  res.Status,
		OrderID: orderID,
		Amount:  res.TransactionAmount,
	}, nil
}

// VerifyWebhook valida el header x-signature ("ts=...,v1=...") con HMAC-SHA256
// sobre el manifest "id:<data.id>;request-id:<x-request-id>;ts:<ts>;".
func (g *MercadoPagoGateway) VerifyWebhook(r *http.Request, dataID string) bool {
	if g.cfg.WebhookSecret == "" {
		return true
	}

	var ts, v1 string
	for _, part := range strings.Split(r.Header.Get("x-signature"), ",") {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		switch key {
		case "ts":
			ts = value
		case "v1":
			v1 = value
		}
	}
	if ts == "" || v1 == "" {
		return false
	}

	var manifest strings.Builder
	if dataID != "" {
		fmt.Fprintf(&manifest, "id:%s;", strings.ToLower(dataID))
	}
	if requestID := r.Header.Get("x-request-id"); requestID != "" {
		fmt.Fprintf(&manifest, "request-id:%s;", requestID)
	}
	fmt.Fprintf(&manifest, "ts:%s;", ts)

	mac := hmac.New(sha256.New, []byte(g.cfg.WebhookSecret))
	mac.Write([]byte(manifest.String()))
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(v1))
}

func (g *MercadoPagoGateway) do(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(payload)
	}

	req, err := http.NewRequestWithContext(ctx, method, mercadoPagoAPI+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+g.cfg.AccessToken)
	req.Header.Set("Content-Type", "application/json")

	res, err := g.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.StatusCode == http.StatusNotFound {
		return ErrPaymentNotFound
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		detail, _ := io.ReadAll(io.LimitReader(res.Body, 1024))
		return fmt.Errorf("mercado pago respondió %d: %s", res.StatusCode, detail)
	}
	return json.NewDecoder(res.Body).Decode(out)
}
