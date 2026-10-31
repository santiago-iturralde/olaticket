package pdf

import (
	"bytes"
	"os"
	"testing"
	"time"
)

func TestGenerateTickets(t *testing.T) {
	out, err := GenerateTickets(OrderTickets{
		OrderID:   42,
		EventName: "Recital Ñandú — Edición Especial",
		Tickets: []Ticket{
			{ID: 7, SectorName: "Platea VIP", Nombre: "Ana Pérez", DNI: "30111222", Code: "OLT-7-abc"},
			{ID: 8, SectorName: "Campo", Nombre: "José Núñez", DNI: "30111333", Code: "OLT-8-def"},
		},
		IssuedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(out, []byte("%PDF-")) {
		t.Fatalf("salida no es un PDF")
	}
	// Una página por entrada
	if pages := bytes.Count(out, []byte("/Type /Page\n")); pages != 2 {
		t.Fatalf("páginas = %d, want 2", pages)
	}
	if p := os.Getenv("PDF_OUT"); p != "" {
		_ = os.WriteFile(p, out, 0o644)
	}
}

func TestGenerateTicketsRequiresCodes(t *testing.T) {
	if _, err := GenerateTickets(OrderTickets{OrderID: 1}); err == nil {
		t.Fatal("se esperaba error sin entradas")
	}
	if _, err := GenerateTickets(OrderTickets{OrderID: 1, Tickets: []Ticket{{ID: 1}}}); err == nil {
		t.Fatal("se esperaba error sin código QR")
	}
}
