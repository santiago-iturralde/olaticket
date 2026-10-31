package recaptcha

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newTestVerifier(t *testing.T, success bool) *Verifier {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil || r.PostForm.Get("secret") != "secret" || r.PostForm.Get("response") == "" {
			t.Errorf("formulario inesperado: %v", r.PostForm)
		}
		if success {
			_, _ = w.Write([]byte(`{"success": true}`))
			return
		}
		_, _ = w.Write([]byte(`{"success": false, "error-codes": ["timeout-or-duplicate"]}`))
	}))
	t.Cleanup(server.Close)

	v := New("secret")
	v.verifyURL = server.URL
	return v
}

func TestVerifyAcceptsValidToken(t *testing.T) {
	if err := newTestVerifier(t, true).Verify(context.Background(), "token", "127.0.0.1"); err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
}

func TestVerifyRejectsInvalidToken(t *testing.T) {
	err := newTestVerifier(t, false).Verify(context.Background(), "token", "")
	if !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("Verify() error = %v, want ErrInvalidToken", err)
	}
}

func TestVerifyRejectsEmptyToken(t *testing.T) {
	if err := New("secret").Verify(context.Background(), "", ""); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("Verify() error = %v, want ErrInvalidToken", err)
	}
}

func TestVerifyDisabledWithoutSecret(t *testing.T) {
	if err := New("").Verify(context.Background(), "", ""); err != nil {
		t.Fatalf("Verify() error = %v, want nil when disabled", err)
	}
}
