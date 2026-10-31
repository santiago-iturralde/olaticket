package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
)

// El QR de cada entrada es "OLT-<ticket_id>-<firma>": el ticket_id solo es adivinable
// (son correlativos), la firma HMAC impide fabricar un QR válido sin el secreto.
// Es corto a propósito para que el QR sea simple y se lea rápido en la puerta.
const entryCodePrefix = "OLT-"

var ErrInvalidEntryCode = errors.New("invalid entry code")

// entryKey deriva una clave propia para los QR: un código de entrada nunca sirve como JWT.
func entryKey() ([]byte, error) {
	secret, err := jwtSecret()
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(append([]byte("olaticket-entry-code:"), secret...))
	return sum[:], nil
}

func entrySignature(key []byte, ticketID int64) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(strconv.FormatInt(ticketID, 10)))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil)[:16])
}

// GenerateEntryCode arma el contenido del QR de una entrada.
func GenerateEntryCode(ticketID int64) (string, error) {
	key, err := entryKey()
	if err != nil {
		return "", err
	}
	return entryCodePrefix + strconv.FormatInt(ticketID, 10) + "-" + entrySignature(key, ticketID), nil
}

// ParseEntryCode valida la firma del QR leído y devuelve su ticket_id.
func ParseEntryCode(code string) (int64, error) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(code), entryCodePrefix)
	if !ok {
		return 0, ErrInvalidEntryCode
	}
	idPart, sig, ok := strings.Cut(rest, "-")
	if !ok {
		return 0, ErrInvalidEntryCode
	}
	ticketID, err := strconv.ParseInt(idPart, 10, 64)
	if err != nil || ticketID <= 0 {
		return 0, ErrInvalidEntryCode
	}
	key, err := entryKey()
	if err != nil {
		return 0, err
	}
	if !hmac.Equal([]byte(sig), []byte(entrySignature(key, ticketID))) {
		return 0, ErrInvalidEntryCode
	}
	return ticketID, nil
}
