package payment

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

// Estados de pago de Mercado Pago que nos interesan.
const (
	StatusApproved  = "approved"
	StatusRejected  = "rejected"
	StatusCancelled = "cancelled"
)

var ErrPaymentNotFound = errors.New("payment not found")

// PreferenceRequest son los datos para generar el link de pago de una orden.
type PreferenceRequest struct {
	OrderID   int64
	EventID   int64
	Items     []PreferenceItem // Una línea por sector del carrito
	ExpiresAt time.Time
}

// PreferenceItem es una línea del cobro: "Evento - Sector" x cantidad.
type PreferenceItem struct {
	ID        string
	Title     string
	Quantity  int
	UnitPrice float64
}

type Preference struct {
	ID        string
	InitPoint string
}

// Payment es el pago tal como lo informa la pasarela (nunca confiamos en el body del webhook).
type Payment struct {
	ID      string
	Status  string
	OrderID int64
	Amount  float64
}

// Gateway abstrae la pasarela: Mercado Pago real o el modo SANDBOX/MOCK local.
type Gateway interface {
	CreatePreference(ctx context.Context, req PreferenceRequest) (*Preference, error)
	GetPayment(ctx context.Context, paymentID string) (*Payment, error)
	// VerifyWebhook valida la firma x-signature de la notificación.
	VerifyWebhook(r *http.Request, dataID string) bool
	IsMock() bool
}

// Config se lee desde el entorno (.env).
type Config struct {
	AccessToken   string // MP_ACCESS_TOKEN
	WebhookSecret string // MP_WEBHOOK_SECRET
	PublicAPIURL  string // PUBLIC_API_URL: URL pública del backend (notification_url)
	FrontendURL   string // FRONTEND_URL: a dónde vuelve el usuario tras pagar
}

func ConfigFromEnv() Config {
	return Config{
		AccessToken:   strings.TrimSpace(os.Getenv("MP_ACCESS_TOKEN")),
		WebhookSecret: strings.TrimSpace(os.Getenv("MP_WEBHOOK_SECRET")),
		PublicAPIURL:  envOr("PUBLIC_API_URL", "http://localhost:8080"),
		FrontendURL:   envOr("FRONTEND_URL", "http://localhost:5173"),
	}
}

// NewGateway devuelve el cliente de Mercado Pago, o el MOCK si no hay token configurado.
func NewGateway(cfg Config) Gateway {
	if cfg.AccessToken == "" {
		log.Println("⚠️  MP_ACCESS_TOKEN no configurado: pagos en modo SANDBOX/MOCK (solo para desarrollo local)")
		return NewMockGateway(cfg)
	}
	if !strings.HasPrefix(cfg.PublicAPIURL, "https://") {
		log.Println("⚠️  PUBLIC_API_URL no es HTTPS: Mercado Pago no podrá enviar webhooks (usá un túnel tipo ngrok)")
	}
	if cfg.WebhookSecret == "" {
		log.Println("⚠️  MP_WEBHOOK_SECRET no configurado: no se valida la firma de los webhooks")
	}
	return NewMercadoPagoGateway(cfg)
}

func envOr(key, fallback string) string {
	if v := strings.TrimRight(strings.TrimSpace(os.Getenv(key)), "/"); v != "" {
		return v
	}
	return fallback
}
