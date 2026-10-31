package mysql

import (
	"database/sql"
	"testing"

	"stock-api/internal/domain"
)

func TestTicketsForItems(t *testing.T) {
	items := []orderItemStock{
		{sectorID: sql.NullInt64{Int64: 1, Valid: true}, cantidad: 2},
		{sectorID: sql.NullInt64{Int64: 2, Valid: true}, cantidad: 1},
	}
	asistentes := []domain.Asistente{
		{SectorID: 2, Nombre: "Carla", DNI: "3"},
		{SectorID: 1, Nombre: "Ana", DNI: "1"},
		{SectorID: 1, Nombre: "Beto", DNI: "2"},
	}

	got := ticketsForItems(items, asistentes, "Comprador", "99")
	want := []domain.Ticket{
		{SectorID: 1, Nombre: "Ana", DNI: "1"},
		{SectorID: 1, Nombre: "Beto", DNI: "2"},
		{SectorID: 2, Nombre: "Carla", DNI: "3"},
	}
	if len(got) != len(want) {
		t.Fatalf("tickets = %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ticket %d = %+v, want %+v", i, got[i], want[i])
		}
	}

	// Sin asistentes, o si no coinciden con los ítems, quedan todas a nombre del comprador.
	for name, a := range map[string][]domain.Asistente{
		"sin asistentes": nil,
		"faltan":         asistentes[:2],
	} {
		got := ticketsForItems(items, a, "Comprador", "99")
		if len(got) != 3 {
			t.Fatalf("%s: %d entradas, want 3", name, len(got))
		}
		for _, ticket := range got {
			if ticket.Nombre != "Comprador" || ticket.DNI != "99" {
				t.Fatalf("%s: entrada %+v no quedó a nombre del comprador", name, ticket)
			}
		}
	}
}
