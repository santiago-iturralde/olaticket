package recaptcha

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const verifyURL = "https://www.google.com/recaptcha/api/siteverify"

// ErrInvalidToken indica que Google rechazó el token (vencido, reutilizado o falso).
var ErrInvalidToken = errors.New("recaptcha token rejected")

// Verifier valida tokens de reCAPTCHA v2 contra Google.
type Verifier struct {
	secret    string
	verifyURL string
	client    *http.Client
}

// FromEnv lee RECAPTCHA_SECRET. Con el secreto vacío el verificador queda deshabilitado
// (solo para desarrollo local, igual que el modo MOCK de pagos).
func FromEnv() *Verifier {
	return New(strings.TrimSpace(os.Getenv("RECAPTCHA_SECRET")))
}

func New(secret string) *Verifier {
	return &Verifier{
		secret:    secret,
		verifyURL: verifyURL,
		client:    &http.Client{Timeout: 5 * time.Second},
	}
}

func (v *Verifier) Enabled() bool {
	return v.secret != ""
}

type verifyResponse struct {
	Success    bool     `json:"success"`
	ErrorCodes []string `json:"error-codes"`
}

// Verify devuelve nil si el token es válido (o si el verificador está deshabilitado),
// ErrInvalidToken si Google lo rechaza, u otro error si no se pudo consultar a Google.
func (v *Verifier) Verify(ctx context.Context, token, remoteIP string) error {
	if !v.Enabled() {
		return nil
	}
	if strings.TrimSpace(token) == "" {
		return ErrInvalidToken
	}

	form := url.Values{"secret": {v.secret}, "response": {token}}
	if remoteIP != "" {
		form.Set("remoteip", remoteIP)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, v.verifyURL, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	res, err := v.client.Do(req)
	if err != nil {
		return fmt.Errorf("error consultando reCAPTCHA: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("reCAPTCHA respondió %d", res.StatusCode)
	}

	var body verifyResponse
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return fmt.Errorf("respuesta de reCAPTCHA inválida: %w", err)
	}
	if !body.Success {
		return fmt.Errorf("%w: %v", ErrInvalidToken, body.ErrorCodes)
	}
	return nil
}
