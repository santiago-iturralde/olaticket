package payment

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http/httptest"
	"testing"
)

func TestVerifyWebhookSignature(t *testing.T) {
	const secret = "webhook-secret"
	gateway := NewMercadoPagoGateway(Config{AccessToken: "APP_USR-x", WebhookSecret: secret})

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("id:123456;request-id:req-1;ts:1704908010;"))
	signature := hex.EncodeToString(mac.Sum(nil))

	req := httptest.NewRequest("POST", "/payments/webhook?type=payment&data.id=123456", nil)
	req.Header.Set("x-request-id", "req-1")
	req.Header.Set("x-signature", "ts=1704908010,v1="+signature)
	if !gateway.VerifyWebhook(req, "123456") {
		t.Fatal("expected valid signature to be accepted")
	}

	req.Header.Set("x-signature", "ts=1704908010,v1=deadbeef")
	if gateway.VerifyWebhook(req, "123456") {
		t.Fatal("expected tampered signature to be rejected")
	}

	req.Header.Del("x-signature")
	if gateway.VerifyWebhook(req, "123456") {
		t.Fatal("expected missing signature to be rejected")
	}
}

func TestMockGetPayment(t *testing.T) {
	gateway := NewMockGateway(Config{})

	p, err := gateway.GetPayment(context.Background(), MockPaymentID(42, StatusApproved))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.OrderID != 42 || p.Status != StatusApproved {
		t.Fatalf("unexpected payment: %+v", p)
	}

	for _, id := range []string{"123", "mock-x-approved", "mock-42-refunded"} {
		if _, err := gateway.GetPayment(context.Background(), id); !errors.Is(err, ErrPaymentNotFound) {
			t.Fatalf("expected ErrPaymentNotFound for %q, got %v", id, err)
		}
	}
}
