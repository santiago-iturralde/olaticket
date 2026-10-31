package auth

import (
	"strings"
	"testing"
)

func TestEntryCode(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret-with-at-least-32-characters")

	code, err := GenerateEntryCode(1234)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(code, "OLT-1234-") || len(code) > 40 {
		t.Fatalf("código inesperado: %q", code)
	}
	if id, err := ParseEntryCode(" " + code + "\n"); err != nil || id != 1234 {
		t.Fatalf("ParseEntryCode() = %d, %v", id, err)
	}

	// Cambiar el ticket_id con la firma de otro, o adulterar la firma, invalida el código.
	forged := strings.Replace(code, "OLT-1234-", "OLT-1235-", 1)
	for _, bad := range []string{forged, code + "x", "1234", "OLT-abc-x", "OLT-0-" + code[9:], ""} {
		if _, err := ParseEntryCode(bad); err == nil {
			t.Errorf("ParseEntryCode(%q) aceptó un código inválido", bad)
		}
	}

	// Con otro secreto, los códigos anteriores dejan de valer.
	t.Setenv("JWT_SECRET", "another-secret-with-at-least-32-characters")
	if _, err := ParseEntryCode(code); err == nil {
		t.Fatal("aceptó un código firmado con otro secreto")
	}
}
