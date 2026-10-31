package pdf

import (
	"bytes"
	"fmt"
	"time"

	"github.com/go-pdf/fpdf"
	qrcode "github.com/skip2/go-qrcode"
)

// OrderTickets son las entradas de una orden: el PDF tiene una página por entrada, cada una
// con su propio QR (personal, se invalida al primer ingreso).
type OrderTickets struct {
	OrderID   int64
	EventName string
	Tickets   []Ticket
	IssuedAt  time.Time
}

// Ticket es una entrada nominativa. Code es el contenido del QR (firmado sobre su ID).
type Ticket struct {
	ID         int64
	SectorName string
	Nombre     string
	DNI        string
	Code       string
}

// GenerateTickets arma el PDF en memoria, con una página A5 por entrada.
func GenerateTickets(data OrderTickets) ([]byte, error) {
	if len(data.Tickets) == 0 {
		return nil, fmt.Errorf("orden #%d sin entradas", data.OrderID)
	}

	doc := fpdf.New("P", "mm", "A5", "")
	doc.SetTitle(fmt.Sprintf("Entradas Olaticket - Orden #%d", data.OrderID), true)
	doc.SetAutoPageBreak(false, 0)
	// Las fuentes estándar usan cp1252: traducimos UTF-8 para que salgan bien los acentos.
	tr := doc.UnicodeTranslatorFromDescriptor("")
	pageW, _ := doc.GetPageSize()

	for i, t := range data.Tickets {
		if t.Code == "" {
			return nil, fmt.Errorf("entrada #%d sin código QR", t.ID)
		}
		qrPNG, err := qrcode.Encode(t.Code, qrcode.Medium, 512)
		if err != nil {
			return nil, fmt.Errorf("error generando el QR de la entrada #%d: %w", t.ID, err)
		}
		doc.AddPage()

		// Encabezado
		doc.SetFillColor(0, 158, 227)
		doc.Rect(0, 0, pageW, 28, "F")
		doc.SetTextColor(255, 255, 255)
		doc.SetFont("Helvetica", "B", 22)
		doc.SetXY(0, 8)
		doc.CellFormat(pageW, 10, "OLATICKET", "", 1, "C", false, 0, "")
		doc.SetFont("Helvetica", "", 10)
		doc.SetX(0)
		doc.CellFormat(pageW, 6, tr(fmt.Sprintf("Entrada %d de %d", i+1, len(data.Tickets))), "", 1, "C", false, 0, "")

		// Evento y titular
		doc.SetTextColor(34, 34, 34)
		doc.SetXY(12, 36)
		doc.SetFont("Helvetica", "B", 16)
		doc.MultiCell(pageW-24, 8, tr(data.EventName), "", "C", false)
		doc.Ln(3)

		row := func(label, value string) {
			doc.SetX(20)
			doc.SetFont("Helvetica", "", 11)
			doc.CellFormat(35, 7, tr(label), "", 0, "L", false, 0, "")
			doc.SetFont("Helvetica", "B", 11)
			doc.CellFormat(pageW-75, 7, tr(value), "", 1, "R", false, 0, "")
		}
		row("Titular", t.Nombre)
		row("DNI", t.DNI)
		if t.SectorName != "" {
			row("Sector", t.SectorName)
		}
		row("Entrada", fmt.Sprintf("#%d", t.ID))
		row("Orden", fmt.Sprintf("#%d", data.OrderID))
		row("Emitida", data.IssuedAt.Format("02/01/2006 15:04"))

		// QR
		const qrSize = 70.0
		qrY := doc.GetY() + 6
		name := fmt.Sprintf("qr-%d", t.ID)
		doc.RegisterImageOptionsReader(name, fpdf.ImageOptions{ImageType: "PNG"}, bytes.NewReader(qrPNG))
		doc.ImageOptions(name, (pageW-qrSize)/2, qrY, qrSize, qrSize, false, fpdf.ImageOptions{ImageType: "PNG"}, 0, "")

		doc.SetXY(12, qrY+qrSize+4)
		doc.SetFont("Helvetica", "", 9)
		doc.SetTextColor(110, 110, 110)
		doc.MultiCell(pageW-24, 5, tr("Presentá este código y tu DNI en el ingreso. Es personal y se invalida al primer uso."), "", "C", false)
	}

	var buf bytes.Buffer
	if err := doc.Output(&buf); err != nil {
		return nil, fmt.Errorf("error generando PDF: %w", err)
	}
	return buf.Bytes(), nil
}
