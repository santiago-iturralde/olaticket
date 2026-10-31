package mysql

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"stock-api/internal/domain"
)

// SetAttendees guarda los asistentes que cargó el comprador. Solo mientras la orden sigue
// reservada (retorna false si no): después del pago ya no se pueden cambiar.
// El estado se lee con FOR UPDATE en vez de condicionar el UPDATE porque MySQL informa
// 0 filas afectadas cuando el valor no cambia (un segundo "Pagar" con los mismos datos).
func (r *Repository) SetAttendees(ctx context.Context, orderID int64, asistentes []domain.Asistente) (bool, error) {
	payload, err := json.Marshal(asistentes)
	if err != nil {
		return false, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("error starting attendees update: %w", err)
	}
	defer tx.Rollback()

	var estado string
	err = tx.QueryRowContext(ctx, "SELECT estado FROM ordenes WHERE id = ? FOR UPDATE", orderID).Scan(&estado)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && estado != domain.OrderReserved) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("error locking order: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "UPDATE ordenes SET asistentes = ? WHERE id = ?", string(payload), orderID); err != nil {
		return false, fmt.Errorf("error saving attendees: %w", err)
	}
	return true, tx.Commit()
}

// ticketsForItems reparte los asistentes entre los ítems de la orden: una entrada por unidad,
// en el orden en que se cargaron. Si no coinciden con los ítems (órdenes reservadas antes de
// pedir asistentes) todas las entradas quedan a nombre del comprador.
func ticketsForItems(items []orderItemStock, asistentes []domain.Asistente, buyerName, buyerDNI string) []domain.Ticket {
	porSector := map[int64][]domain.Asistente{}
	for _, a := range asistentes {
		porSector[a.SectorID] = append(porSector[a.SectorID], a)
	}
	match := len(asistentes) > 0
	total := 0
	for _, item := range items {
		total += item.cantidad
		if len(porSector[item.sectorID.Int64]) != item.cantidad {
			match = false
		}
	}

	tickets := make([]domain.Ticket, 0, total)
	for _, item := range items {
		for i := 0; i < item.cantidad; i++ {
			t := domain.Ticket{SectorID: item.sectorID.Int64, Nombre: buyerName, DNI: buyerDNI}
			if match {
				a := porSector[item.sectorID.Int64][i]
				t.Nombre, t.DNI = a.Nombre, a.DNI
			}
			tickets = append(tickets, t)
		}
	}
	return tickets
}

// ConfirmOrder marca como pagada una orden que sigue reservada y crea sus entradas
// nominativas en la misma transacción: nunca hay una orden pagada sin entradas.
// Retorna false si la orden ya no estaba en 'reservada' (expiró, se rechazó o ya se confirmó).
func (r *Repository) ConfirmOrder(ctx context.Context, orderID int64, paymentID, qrToken string) (bool, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("error starting confirmation: %w", err)
	}
	defer tx.Rollback()

	// Se bloquea solo la orden, sin JOIN: TiDB no soporta "FOR UPDATE OF" y un FOR UPDATE
	// sobre el JOIN bloquearía también al usuario.
	var asistentesJSON sql.NullString
	var userID int64
	err = tx.QueryRowContext(
		ctx,
		"SELECT asistentes, usuario_id FROM ordenes WHERE id = ? AND estado = 'reservada' FOR UPDATE",
		orderID,
	).Scan(&asistentesJSON, &userID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("error locking order to confirm: %w", err)
	}

	// Titular de respaldo para las entradas sin asistente cargado (sin bloquear al usuario).
	buyerName, buyerDNI := "Titular", ""
	err = tx.QueryRowContext(
		ctx,
		`SELECT COALESCE(NULLIF(TRIM(CONCAT(nombre, ' ', apellido)), ''), email, 'Titular'), COALESCE(numero_dni, '')
		 FROM usuarios WHERE id = ?`,
		userID,
	).Scan(&buyerName, &buyerDNI)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("error reading buyer: %w", err)
	}
	var asistentes []domain.Asistente
	if asistentesJSON.Valid {
		if err := json.Unmarshal([]byte(asistentesJSON.String), &asistentes); err != nil {
			return false, fmt.Errorf("invalid attendees in order %d: %w", orderID, err)
		}
	}

	if _, err := tx.ExecContext(
		ctx,
		`UPDATE ordenes
		 SET estado = 'confirmada', payment_id = ?, qr_token = ?, reservada_hasta = NULL
		 WHERE id = ?`,
		paymentID, qrToken, orderID,
	); err != nil {
		return false, fmt.Errorf("error confirming order: %w", err)
	}

	items, err := orderItemsBySector(ctx, tx, orderID)
	if err != nil {
		return false, err
	}
	tickets := ticketsForItems(items, asistentes, buyerName, buyerDNI)
	if len(tickets) > 0 {
		values := make([]string, len(tickets))
		args := make([]any, 0, len(tickets)*4)
		for i, t := range tickets {
			values[i] = "(?, NULLIF(?, 0), ?, ?)"
			args = append(args, orderID, t.SectorID, t.Nombre, t.DNI)
		}
		if _, err := tx.ExecContext(
			ctx,
			"INSERT INTO tickets (orden_id, sector_id, nombre, dni) VALUES "+strings.Join(values, ", "),
			args...,
		); err != nil {
			return false, fmt.Errorf("error creating tickets: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("error committing confirmation: %w", err)
	}
	return true, nil
}

const ticketColumns = `t.id, t.orden_id, COALESCE(t.sector_id, 0), COALESCE(s.nombre, ''), t.nombre, t.dni,
	t.ingresado, t.ingresado_en`

func scanTicket(row rowScanner, extra ...any) (domain.Ticket, error) {
	var t domain.Ticket
	var ingresadoEn sql.NullTime
	dest := append([]any{&t.ID, &t.OrderID, &t.SectorID, &t.SectorName, &t.Nombre, &t.DNI, &t.Ingresado, &ingresadoEn}, extra...)
	if err := row.Scan(dest...); err != nil {
		return t, err
	}
	if ingresadoEn.Valid {
		t.IngresadoEn = &ingresadoEn.Time
	}
	return t, nil
}

// GetOrderTickets lista las entradas nominativas de una orden (vacío hasta que se paga).
func (r *Repository) GetOrderTickets(ctx context.Context, orderID int64) ([]domain.Ticket, error) {
	rows, err := r.db.QueryContext(
		ctx,
		"SELECT "+ticketColumns+` FROM tickets t LEFT JOIN sectores s ON s.id = t.sector_id
		 WHERE t.orden_id = ? ORDER BY t.id`,
		orderID,
	)
	if err != nil {
		return nil, fmt.Errorf("error listing tickets: %w", err)
	}
	defer rows.Close()

	tickets := []domain.Ticket{}
	for rows.Next() {
		t, err := scanTicket(rows)
		if err != nil {
			return nil, err
		}
		tickets = append(tickets, t)
	}
	return tickets, rows.Err()
}

// ScanTicket registra el ingreso de una entrada en la puerta del evento indicado.
// La fila se bloquea (FOR UPDATE): si dos lectores escanean el mismo QR a la vez, uno
// entra y el otro recibe ErrTicketAlreadyUsed. Ante un rechazo devuelve igual los datos
// de la entrada (salvo si es de otro evento) para que la puerta vea a quién pertenece.
func (r *Repository) ScanTicket(ctx context.Context, ticketID, eventID int64) (*domain.ScanResult, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("error starting scan: %w", err)
	}
	defer tx.Rollback()

	// Primero se bloquea solo la fila del ticket (TiDB no soporta "FOR UPDATE OF"). El estado
	// de ingreso sale de esta lectura bloqueante: es siempre el último confirmado, aunque otro
	// lector lo haya marcado mientras esperábamos el lock (en TiDB una lectura común no lo vería).
	var ingresado bool
	var ingresadoEn sql.NullTime
	err = tx.QueryRowContext(ctx, "SELECT ingresado, ingresado_en FROM tickets WHERE id = ? FOR UPDATE", ticketID).
		Scan(&ingresado, &ingresadoEn)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrTicketNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("error locking ticket: %w", err)
	}

	var res domain.ScanResult
	res.Ticket, err = scanTicket(tx.QueryRowContext(
		ctx,
		"SELECT "+ticketColumns+`, o.evento_id, o.estado
		 FROM tickets t
		 JOIN ordenes o ON o.id = t.orden_id
		 LEFT JOIN sectores s ON s.id = t.sector_id
		 WHERE t.id = ?`,
		ticketID,
	), &res.EventID, &res.OrderStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrTicketNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("error reading ticket: %w", err)
	}
	res.Ingresado, res.IngresadoEn = ingresado, nil
	if ingresadoEn.Valid {
		res.IngresadoEn = &ingresadoEn.Time
	}

	switch {
	case res.EventID != eventID:
		return nil, domain.ErrTicketOtherEvent
	case res.OrderStatus != domain.OrderConfirmed:
		return &res, domain.ErrTicketNotPaid
	case res.Ingresado:
		return &res, domain.ErrTicketAlreadyUsed
	}

	now := time.Now()
	if _, err := tx.ExecContext(ctx, "UPDATE tickets SET ingresado = TRUE, ingresado_en = ? WHERE id = ?", now, ticketID); err != nil {
		return nil, fmt.Errorf("error registering entry: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("error committing scan: %w", err)
	}
	res.Ingresado, res.IngresadoEn = true, &now
	return &res, nil
}
