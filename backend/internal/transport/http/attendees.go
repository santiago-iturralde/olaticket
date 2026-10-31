package http

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"stock-api/internal/domain"
	"stock-api/internal/platform/auth"
)

// Columnas tickets.nombre VARCHAR(100) y tickets.dni VARCHAR(20).
const (
	minAttendeeNameLength = 3
	maxAttendeeNameLength = 100
)

// DNI argentino (7-8 dígitos) o documento extranjero alfanumérico; los puntos, guiones y
// espacios de "30.111.222" se descartan.
var (
	documentSeparators = strings.NewReplacer(".", "", "-", "", " ", "")
	documentPattern    = regexp.MustCompile(`^[A-Z0-9]{6,12}$`)
)

type asistenteRequest struct {
	SectorID int64  `json:"sector_id"`
	Nombre   string `json:"nombre"`
	DNI      string `json:"dni"`
}

func attendeeField(i int, name string) string {
	return fmt.Sprintf("asistentes.%d.%s", i, name)
}

func normalizeDocument(dni string) string {
	return strings.ToUpper(documentSeparators.Replace(strings.TrimSpace(dni)))
}

func validAttendeeName(nombre string) bool {
	n := utf8.RuneCountInString(nombre)
	if n < minAttendeeNameLength || n > maxAttendeeNameLength {
		return false
	}
	return strings.IndexFunc(nombre, unicode.IsLetter) >= 0
}

// validateAttendees controla que haya una persona por entrada de la orden (la misma
// cantidad por sector), con nombre y documento válidos y sin documentos repetidos.
func validateAttendees(order *domain.Order, reqs []asistenteRequest) ([]domain.Asistente, *fieldError) {
	if total := order.Quantity(); len(reqs) != total {
		return nil, &fieldError{"asistentes", fmt.Sprintf("Cargá los datos de las %d entradas", total)}
	}
	faltan := map[int64]int{}
	for _, item := range order.Items {
		faltan[item.SectorID] += item.Quantity
	}

	asistentes := make([]domain.Asistente, len(reqs))
	documentos := map[string]bool{}
	for i, req := range reqs {
		nombre := strings.Join(strings.Fields(req.Nombre), " ")
		dni := normalizeDocument(req.DNI)
		switch {
		case faltan[req.SectorID] == 0:
			return nil, &fieldError{"asistentes", "Los asistentes no coinciden con las entradas de la orden. Recargá la página"}
		case !validAttendeeName(nombre):
			return nil, &fieldError{attendeeField(i, "nombre"), "Ingresá nombre y apellido"}
		case !documentPattern.MatchString(dni):
			return nil, &fieldError{attendeeField(i, "dni"), "Ingresá un DNI válido (solo números)"}
		case documentos[dni]:
			return nil, &fieldError{attendeeField(i, "dni"), "Ese DNI ya está en otra entrada de esta compra"}
		}
		faltan[req.SectorID]--
		documentos[dni] = true
		asistentes[i] = domain.Asistente{SectorID: req.SectorID, Nombre: nombre, DNI: dni}
	}
	return asistentes, nil
}

// ticketView es una entrada tal como la ve su dueño: con el contenido del QR.
type ticketView struct {
	domain.Ticket
	Codigo string `json:"codigo"`
}

func ticketViews(tickets []domain.Ticket) ([]ticketView, error) {
	views := make([]ticketView, len(tickets))
	for i, t := range tickets {
		code, err := auth.GenerateEntryCode(t.ID)
		if err != nil {
			return nil, err
		}
		views[i] = ticketView{Ticket: t, Codigo: code}
	}
	return views, nil
}
