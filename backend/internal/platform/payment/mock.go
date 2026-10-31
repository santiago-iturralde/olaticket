package payment

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// MockGateway simula Mercado Pago para desarrollo local (sin MP_ACCESS_TOKEN).
// El init_point apunta a una página del propio backend con botones Aprobar/Rechazar,
// que dispara el mismo flujo del webhook real.
type MockGateway struct {
	cfg Config
}

func NewMockGateway(cfg Config) *MockGateway {
	return &MockGateway{cfg: cfg}
}

func (g *MockGateway) IsMock() bool { return true }

func (g *MockGateway) CreatePreference(_ context.Context, req PreferenceRequest) (*Preference, error) {
	return &Preference{
		ID:        fmt.Sprintf("mock-pref-%d", req.OrderID),
		InitPoint: fmt.Sprintf("%s/payments/mock/checkout?order_id=%d", g.cfg.PublicAPIURL, req.OrderID),
	}, nil
}

// MockPaymentID codifica orden y resultado en el ID: "mock-<orderID>-<status>".
func MockPaymentID(orderID int64, status string) string {
	return fmt.Sprintf("mock-%d-%s", orderID, status)
}

func (g *MockGateway) GetPayment(_ context.Context, paymentID string) (*Payment, error) {
	parts := strings.SplitN(paymentID, "-", 3)
	if len(parts) != 3 || parts[0] != "mock" {
		return nil, ErrPaymentNotFound
	}
	orderID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return nil, ErrPaymentNotFound
	}
	switch parts[2] {
	case StatusApproved, StatusRejected, StatusCancelled:
	default:
		return nil, ErrPaymentNotFound
	}
	return &Payment{ID: paymentID, Status: parts[2], OrderID: orderID}, nil
}

func (g *MockGateway) VerifyWebhook(*http.Request, string) bool { return true }
