package mysql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"stock-api/internal/domain"
	"strings"

	mysqlDriver "github.com/go-sql-driver/mysql"
)

// Repository implementa la interfaz domain.EventRepository para MySQL.
type Repository struct {
	db *sql.DB
}

// NewRepository crea una nueva instancia del repositorio.
func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

// selectEventWithOrganizador trae el evento junto con los datos básicos de su organizador.
// Se usa con scanEvent para que ambos queden siempre alineados.
const selectEventWithOrganizador = eventColumns + `
	FROM eventos e
	JOIN organizadores o ON o.id = e.organizador_id`

const eventColumns = `
	SELECT e.id, e.nombre, e.limite_tickets_por_orden, COALESCE(e.portada_url, ''), COALESCE(e.mapa_url, ''),
	       o.id, o.nombre, o.email_contacto, o.telefono`

// rowScanner abarca *sql.Row y *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

// extra recibe los destinos de columnas adicionales al final del SELECT.
// Los sectores se cargan aparte con loadSectores.
func scanEvent(row rowScanner, extra ...any) (domain.Event, error) {
	var e domain.Event
	dest := append([]any{
		&e.ID, &e.Nombre, &e.LimiteTicketsPorOrden, &e.PortadaURL, &e.MapaURL,
		&e.Organizador.ID, &e.Organizador.Nombre, &e.Organizador.EmailContacto, &e.Organizador.Telefono,
	}, extra...)
	err := row.Scan(dest...)
	e.OrganizadorID = e.Organizador.ID
	return e, err
}

// GetByID busca un evento por su ID en la tabla 'eventos', con su organizador.
func (r *Repository) GetByID(ctx context.Context, id int64) (*domain.Event, error) {
	row := r.db.QueryRowContext(ctx, selectEventWithOrganizador+" WHERE e.id = ?", id)

	e, err := scanEvent(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, domain.ErrEventNotFound
		}
		return nil, fmt.Errorf("error fetching event: %w", err)
	}
	if err := r.loadSectores(ctx, &e); err != nil {
		return nil, err
	}
	return &e, nil
}

// loadSectores carga los sectores de los eventos indicados con una sola query,
// en el orden en que el organizador los dio de alta.
func (r *Repository) loadSectores(ctx context.Context, eventos ...*domain.Event) error {
	if len(eventos) == 0 {
		return nil
	}
	ids := make([]any, len(eventos))
	for i, e := range eventos {
		ids[i] = e.ID
	}
	rows, err := r.db.QueryContext(
		ctx,
		`SELECT id, evento_id, nombre, precio, capacidad, stock_disponible
		 FROM sectores WHERE evento_id IN (?`+strings.Repeat(",?", len(ids)-1)+`)
		 ORDER BY evento_id, id`,
		ids...,
	)
	if err != nil {
		return fmt.Errorf("error listing sectores: %w", err)
	}
	defer rows.Close()

	porEvento := make(map[int64][]domain.Sector, len(eventos))
	for rows.Next() {
		var s domain.Sector
		if err := rows.Scan(&s.ID, &s.EventoID, &s.Nombre, &s.Precio, &s.Capacidad, &s.StockDisponible); err != nil {
			return err
		}
		porEvento[s.EventoID] = append(porEvento[s.EventoID], s)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, e := range eventos {
		e.SetSectores(porEvento[e.ID])
	}
	return nil
}

// CreateEvent da de alta un evento con sus sectores (todo el cupo disponible) en una sola
// transacción y retorna el ID generado.
func (r *Repository) CreateEvent(ctx context.Context, e domain.Event) (int64, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("error starting event create: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(
		ctx,
		`INSERT INTO eventos (nombre, limite_tickets_por_orden, portada_url, mapa_url, organizador_id)
		 VALUES (?, ?, NULLIF(?, ''), NULLIF(?, ''), ?)`,
		e.Nombre, e.LimiteTicketsPorOrden, e.PortadaURL, e.MapaURL, e.OrganizadorID,
	)
	if err != nil {
		var mysqlErr *mysqlDriver.MySQLError
		if errors.As(err, &mysqlErr) && mysqlErr.Number == 1452 { // FK: el organizador no existe
			return 0, domain.ErrOrganizadorNotFound
		}
		return 0, fmt.Errorf("error creating event: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("error getting event id: %w", err)
	}
	for _, s := range e.Sectores {
		if err := insertSector(ctx, tx, id, s.Nombre, s.Precio, s.Capacidad); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("error committing event create: %w", err)
	}
	return id, nil
}

func insertSector(ctx context.Context, tx *sql.Tx, eventID int64, nombre string, precio float64, capacidad int) error {
	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO sectores (evento_id, nombre, precio, capacidad, stock_disponible)
		 VALUES (?, ?, ?, ?, ?)`,
		eventID, nombre, precio, capacidad, capacidad,
	); err != nil {
		return fmt.Errorf("error creating sector %q: %w", nombre, err)
	}
	return nil
}

// UpdateEvent aplica los cambios del backoffice. Si organizadorID > 0 solo puede tocar
// eventos de ese organizador (un evento ajeno se reporta como inexistente).
// Si vienen sectores, reemplazan a los actuales:
//   - con ID: se editan; cambiar la capacidad mueve stock_disponible en la misma diferencia,
//     así las entradas ya vendidas o reservadas siguen descontadas.
//   - sin ID: se crean con todo el cupo disponible.
//   - los existentes que no vienen se eliminan, solo si no tienen nada vendido ni reservado.
//
// El evento y sus sectores se bloquean (FOR UPDATE) para que una reserva concurrente no se
// pise con la edición.
func (r *Repository) UpdateEvent(ctx context.Context, id, organizadorID int64, upd domain.EventUpdate) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("error starting event update: %w", err)
	}
	defer tx.Rollback()

	var lockedID int64
	err = tx.QueryRowContext(
		ctx,
		"SELECT id FROM eventos WHERE id = ? AND (? = 0 OR organizador_id = ?) FOR UPDATE",
		id, organizadorID, organizadorID,
	).Scan(&lockedID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.ErrEventNotFound
		}
		return fmt.Errorf("error locking event: %w", err)
	}

	if _, err := tx.ExecContext(
		ctx,
		`UPDATE eventos
		 SET nombre = COALESCE(?, nombre), limite_tickets_por_orden = COALESCE(?, limite_tickets_por_orden),
		     portada_url = IF(? IS NULL, portada_url, NULLIF(?, '')),
		     mapa_url = IF(? IS NULL, mapa_url, NULLIF(?, ''))
		 WHERE id = ?`,
		upd.Nombre, upd.LimiteTicketsPorOrden, upd.PortadaURL, upd.PortadaURL, upd.MapaURL, upd.MapaURL, id,
	); err != nil {
		return fmt.Errorf("error updating event: %w", err)
	}

	if upd.Sectores != nil {
		if err := replaceSectores(ctx, tx, id, upd.Sectores); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func replaceSectores(ctx context.Context, tx *sql.Tx, eventID int64, sectores []domain.SectorInput) error {
	rows, err := tx.QueryContext(
		ctx,
		"SELECT id, nombre, capacidad, stock_disponible FROM sectores WHERE evento_id = ? FOR UPDATE",
		eventID,
	)
	if err != nil {
		return fmt.Errorf("error locking sectores: %w", err)
	}
	actuales := map[int64]domain.Sector{}
	for rows.Next() {
		var s domain.Sector
		if err := rows.Scan(&s.ID, &s.Nombre, &s.Capacidad, &s.StockDisponible); err != nil {
			rows.Close()
			return err
		}
		actuales[s.ID] = s
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for i, in := range sectores {
		if in.ID == 0 {
			if err := insertSector(ctx, tx, eventID, in.Nombre, in.Precio, in.Capacidad); err != nil {
				return err
			}
			continue
		}
		actual, ok := actuales[in.ID]
		if !ok { // De otro evento, ya eliminado o repetido en la lista
			return &domain.SectorError{Index: i, Nombre: in.Nombre, Err: domain.ErrSectorNotFound}
		}
		delete(actuales, in.ID)

		stock := actual.StockDisponible + (in.Capacidad - actual.Capacidad)
		if stock < 0 {
			return &domain.SectorError{Index: i, Nombre: in.Nombre, Err: domain.ErrTotalBelowSold}
		}
		if _, err := tx.ExecContext(
			ctx,
			"UPDATE sectores SET nombre = ?, precio = ?, capacidad = ?, stock_disponible = ? WHERE id = ?",
			in.Nombre, in.Precio, in.Capacidad, stock, in.ID,
		); err != nil {
			return fmt.Errorf("error updating sector %d: %w", in.ID, err)
		}
	}

	// Los que quedaron no vinieron en la lista: se eliminan si nunca descontaron stock.
	// Las órdenes en la fila (pendientes) de ese sector se rechazan solas al no encontrarlo.
	for _, s := range actuales {
		if s.StockDisponible != s.Capacidad {
			return &domain.SectorError{Index: -1, Nombre: s.Nombre, Err: domain.ErrSectorHasSales}
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM sectores WHERE id = ?", s.ID); err != nil {
			return fmt.Errorf("error deleting sector %d: %w", s.ID, err)
		}
	}
	return nil
}

// CreateOrder guarda la orden inicial con estado 'pendiente' y sus ítems (sector y cantidad
// de cada línea del carrito) en una transacción, y retorna el ID generado.
func (r *Repository) CreateOrder(ctx context.Context, eventID, userID int64, items []domain.OrderItemInput) (int64, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("error starting order create: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, "INSERT INTO ordenes (evento_id, usuario_id, estado) VALUES (?, ?, 'pendiente')", eventID, userID)
	if err != nil {
		return 0, fmt.Errorf("error creating order: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("error getting last insert id: %w", err)
	}

	values := make([]string, len(items))
	args := make([]any, 0, len(items)*3)
	for i, item := range items {
		values[i] = "(?, ?, ?)"
		args = append(args, id, item.SectorID, item.Cantidad)
	}
	if _, err := tx.ExecContext(
		ctx,
		"INSERT INTO orden_items (orden_id, sector_id, cantidad) VALUES "+strings.Join(values, ", "),
		args...,
	); err != nil {
		return 0, fmt.Errorf("error creating order items: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("error committing order create: %w", err)
	}
	return id, nil
}

// orderItemStock es lo que reservar o liberar una orden mueve en cada sector.
type orderItemStock struct {
	sectorID sql.NullInt64
	cantidad int
}

// orderItemsBySector lee los ítems de la orden ordenados por sector_id: reservas y liberaciones
// tocan los sectores siempre de menor a mayor, así dos transacciones con sectores en común
// se esperan en vez de bloquearse mutuamente (deadlock).
func orderItemsBySector(ctx context.Context, tx *sql.Tx, orderID int64) ([]orderItemStock, error) {
	rows, err := tx.QueryContext(
		ctx,
		"SELECT sector_id, cantidad FROM orden_items WHERE orden_id = ? ORDER BY sector_id",
		orderID,
	)
	if err != nil {
		return nil, fmt.Errorf("error reading order items: %w", err)
	}
	defer rows.Close()

	var items []orderItemStock
	for rows.Next() {
		var item orderItemStock
		if err := rows.Scan(&item.sectorID, &item.cantidad); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// UpdateOrderStatus persists the final order status and ticket token together.
func (r *Repository) UpdateOrderStatus(ctx context.Context, orderID int64, status, qrToken string) error {
	query := "UPDATE ordenes SET estado = ?, qr_token = NULLIF(?, '') WHERE id = ?"
	_, err := r.db.ExecContext(ctx, query, status, qrToken, orderID)
	if err != nil {
		return fmt.Errorf("error updating order status: %w", err)
	}
	return nil
}

func (r *Repository) GetOrderStatus(ctx context.Context, orderID int64) (*domain.OrderStatus, error) {
	var order domain.OrderStatus
	err := r.db.QueryRowContext(
		ctx,
		"SELECT estado, COALESCE(qr_token, ''), usuario_id, evento_id FROM ordenes WHERE id = ?",
		orderID,
	).Scan(&order.Status, &order.QRToken, &order.UserID, &order.EventID)
	if err != nil {
		return nil, err
	}
	return &order, nil
}

// GetOrder busca una orden por su ID para ver su estado
func (r *Repository) GetOrder(ctx context.Context, orderID int64) (string, error) {
	query := "SELECT estado FROM ordenes WHERE id = ?"
	var estado string

	err := r.db.QueryRowContext(ctx, query, orderID).Scan(&estado)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", fmt.Errorf("orden no encontrada")
		}
		return "", err
	}
	return estado, nil
}

// GetAllEvents devuelve la lista de todos los eventos disponibles, con su organizador
func (r *Repository) GetAllEvents(ctx context.Context) ([]domain.Event, error) {
	eventos, err := r.queryEvents(ctx, selectEventWithOrganizador+" ORDER BY e.id")
	if err != nil {
		return nil, err
	}
	ptrs := make([]*domain.Event, len(eventos))
	for i := range eventos {
		ptrs[i] = &eventos[i]
	}
	return eventos, r.loadSectores(ctx, ptrs...)
}

// GetEventsByOrganizador lista los eventos de un organizador (0 = todos, para el admin)
// con lo efectivamente vendido: órdenes 'confirmada' al precio congelado en cada una.
// Las reservas no cuentan aunque ya hayan descontado stock.
func (r *Repository) GetEventsByOrganizador(ctx context.Context, organizadorID int64) ([]domain.EventSummary, error) {
	rows, err := r.db.QueryContext(
		ctx,
		eventColumns+`, COALESCE(v.vendidas, 0), COALESCE(v.recaudacion, 0)
		FROM eventos e
		JOIN organizadores o ON o.id = e.organizador_id
		LEFT JOIN (
			SELECT o.evento_id, SUM(i.cantidad) AS vendidas, SUM(i.cantidad * i.precio_unitario) AS recaudacion
			FROM ordenes o
			JOIN orden_items i ON i.orden_id = o.id
			WHERE o.estado = 'confirmada'
			GROUP BY o.evento_id
		) v ON v.evento_id = e.id
		WHERE (? = 0 OR e.organizador_id = ?)
		ORDER BY e.id`,
		organizadorID, organizadorID,
	)
	if err != nil {
		return nil, fmt.Errorf("error listing organizador events: %w", err)
	}
	defer rows.Close()

	eventos := []domain.EventSummary{} // Vacío = [] en el JSON, no null
	for rows.Next() {
		var summary domain.EventSummary
		e, err := scanEvent(rows, &summary.EntradasVendidas, &summary.Recaudacion)
		if err != nil {
			return nil, err
		}
		summary.Event = e
		eventos = append(eventos, summary)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close() // Antes de la siguiente query, para no retener la conexión

	ptrs := make([]*domain.Event, len(eventos))
	for i := range eventos {
		ptrs[i] = &eventos[i].Event
	}
	return eventos, r.loadSectores(ctx, ptrs...)
}

// DeleteEvent elimina un evento sin ventas. Si organizadorID > 0 solo puede borrar eventos
// de ese organizador (uno ajeno se reporta como inexistente). Las órdenes pendientes o
// reservadas pasan a 'rechazada' y se devuelven sus IDs para avisar a los compradores;
// el resto del historial de órdenes se conserva.
// Bloquea el evento y todas sus órdenes (FOR UPDATE sobre idx_ordenes_evento_estado): un
// pago que se confirma en paralelo o termina antes (y el evento no se borra) o encuentra la
// orden ya rechazada (y el webhook lo registra para reembolso manual).
func (r *Repository) DeleteEvent(ctx context.Context, id, organizadorID int64) ([]int64, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("error starting event delete: %w", err)
	}
	defer tx.Rollback()

	var lockedID int64
	err = tx.QueryRowContext(
		ctx,
		"SELECT id FROM eventos WHERE id = ? AND (? = 0 OR organizador_id = ?) FOR UPDATE",
		id, organizadorID, organizadorID,
	).Scan(&lockedID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrEventNotFound
		}
		return nil, fmt.Errorf("error locking event: %w", err)
	}

	rows, err := tx.QueryContext(ctx, "SELECT id, estado FROM ordenes WHERE evento_id = ? FOR UPDATE", id)
	if err != nil {
		return nil, fmt.Errorf("error locking event orders: %w", err)
	}
	var cancelled []int64
	hasSales := false
	for rows.Next() {
		var orderID int64
		var estado string
		if err := rows.Scan(&orderID, &estado); err != nil {
			rows.Close()
			return nil, err
		}
		switch estado {
		case domain.OrderConfirmed:
			hasSales = true
		case domain.OrderPending, domain.OrderReserved:
			cancelled = append(cancelled, orderID)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if hasSales {
		return nil, domain.ErrEventHasSales
	}

	if _, err := tx.ExecContext(
		ctx,
		`UPDATE ordenes SET estado = 'rechazada', reservada_hasta = NULL
		 WHERE evento_id = ? AND estado IN ('pendiente', 'reservada')`,
		id,
	); err != nil {
		return nil, fmt.Errorf("error cancelling event orders: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM eventos WHERE id = ?", id); err != nil {
		return nil, fmt.Errorf("error deleting event: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("error committing event delete: %w", err)
	}
	return cancelled, nil
}

func (r *Repository) queryEvents(ctx context.Context, query string, args ...any) ([]domain.Event, error) {
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	eventos := []domain.Event{} // Vacío = [] en el JSON, no null (la home hace .map)
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		eventos = append(eventos, e)
	}
	return eventos, rows.Err()
}

// GetOrganizadores lista todas las productoras (para el selector del admin).
func (r *Repository) GetOrganizadores(ctx context.Context) ([]domain.Organizador, error) {
	rows, err := r.db.QueryContext(ctx, "SELECT id, nombre, email_contacto, telefono FROM organizadores ORDER BY nombre")
	if err != nil {
		return nil, fmt.Errorf("error listing organizadores: %w", err)
	}
	defer rows.Close()

	organizadores := []domain.Organizador{}
	for rows.Next() {
		var o domain.Organizador
		if err := rows.Scan(&o.ID, &o.Nombre, &o.EmailContacto, &o.Telefono); err != nil {
			return nil, err
		}
		organizadores = append(organizadores, o)
	}
	return organizadores, rows.Err()
}

// GetQueuePosition calcula cuántas personas hay delante de una orden específica
func (r *Repository) GetQueuePosition(ctx context.Context, orderID int64, eventID int64) (int, error) {
	// La lógica es: Contar cuántas órdenes están "pendientes" para este evento
	// Y que tengan un ID MENOR al mío (o sea, llegaron antes).
	query := `
        SELECT COUNT(*) 
        FROM ordenes 
        WHERE evento_id = ? AND estado = 'pendiente' AND id < ?
    `
	var position int
	err := r.db.QueryRowContext(ctx, query, eventID, orderID).Scan(&position)
	if err != nil {
		return 0, err
	}
	// Si hay 5 personas antes, yo soy el puesto 6.
	return position + 1, nil
}

// ReserveStock descuenta el stock de todos los sectores de la orden y la deja 'reservada' en
// una sola transacción: o se reservan todos los ítems o ninguno, y nunca queda stock
// descontado sin una orden que lo respalde. También congela en cada ítem el precio de su
// sector: es lo que se cobra aunque después se edite. Un sector inexistente o de otro
// evento se trata como agotado.
func (r *Repository) ReserveStock(ctx context.Context, orderID, eventID int64, ttlSeconds int) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("error starting reservation: %w", err)
	}
	defer tx.Rollback()

	// Lock sobre el evento: mantiene el orden evento -> sectores -> orden que usan
	// UpdateEvent y DeleteEvent, así una edición o un borrado en paralelo espera a la
	// reserva (o al revés) en vez de terminar en deadlock. Es FOR UPDATE y no FOR SHARE
	// porque TiDB no implementa los locks compartidos (Error 1235); con un solo worker
	// reservando, que sea exclusivo no frena nada.
	var lockedID int64
	err = tx.QueryRowContext(ctx, "SELECT id FROM eventos WHERE id = ? FOR UPDATE", eventID).Scan(&lockedID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ErrNotEnoughStock // El evento se eliminó mientras la orden esperaba en la fila
	}
	if err != nil {
		return fmt.Errorf("error locking event: %w", err)
	}

	items, err := orderItemsBySector(ctx, tx, orderID)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		return fmt.Errorf("order %d has no items", orderID)
	}

	// De menor a mayor sector_id (ver orderItemsBySector): dos carritos con sectores en
	// común bloquean las filas en el mismo orden.
	for _, item := range items {
		result, err := tx.ExecContext(
			ctx,
			`UPDATE sectores SET stock_disponible = stock_disponible - ?
			 WHERE id = ? AND evento_id = ? AND stock_disponible >= ?`,
			item.cantidad, item.sectorID, eventID, item.cantidad,
		)
		if err != nil {
			return fmt.Errorf("error decreasing stock: %w", err)
		}
		if rows, err := result.RowsAffected(); err != nil {
			return fmt.Errorf("error checking affected rows: %w", err)
		} else if rows == 0 {
			return domain.ErrNotEnoughStock // El rollback devuelve lo ya descontado de otros sectores
		}
	}

	if _, err := tx.ExecContext(
		ctx,
		`UPDATE orden_items i JOIN sectores s ON s.id = i.sector_id
		 SET i.precio_unitario = s.precio
		 WHERE i.orden_id = ?`,
		orderID,
	); err != nil {
		return fmt.Errorf("error freezing item prices: %w", err)
	}

	result, err := tx.ExecContext(
		ctx,
		`UPDATE ordenes
		 SET estado = 'reservada', reservada_hasta = DATE_ADD(NOW(), INTERVAL ? SECOND)
		 WHERE id = ? AND estado = 'pendiente'`,
		ttlSeconds, orderID,
	)
	if err != nil {
		return fmt.Errorf("error reserving order: %w", err)
	}
	if rows, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("error checking reserved order: %w", err)
	} else if rows == 0 {
		return fmt.Errorf("order %d is not pending", orderID)
	}

	return tx.Commit()
}

// ReleaseReservation pasa una orden 'reservada' al estado indicado (expirada/rechazada)
// y devuelve el stock de cada ítem a su sector. Retorna false si la orden ya no estaba
// reservada, lo que hace la operación idempotente frente a webhooks repetidos y al expirador.
func (r *Repository) ReleaseReservation(ctx context.Context, orderID int64, status string) (bool, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("error starting release: %w", err)
	}
	defer tx.Rollback()

	var lockedID int64
	err = tx.QueryRowContext(
		ctx,
		"SELECT id FROM ordenes WHERE id = ? AND estado = 'reservada' FOR UPDATE",
		orderID,
	).Scan(&lockedID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("error locking reserved order: %w", err)
	}

	items, err := orderItemsBySector(ctx, tx, orderID)
	if err != nil {
		return false, err
	}

	if _, err := tx.ExecContext(
		ctx,
		"UPDATE ordenes SET estado = ?, reservada_hasta = NULL WHERE id = ?",
		status, orderID,
	); err != nil {
		return false, fmt.Errorf("error releasing order: %w", err)
	}
	// Mismo orden de sectores que ReserveStock. Si el sector ya no existe (evento
	// eliminado) no hay stock que devolver.
	for _, item := range items {
		if _, err := tx.ExecContext(
			ctx,
			"UPDATE sectores SET stock_disponible = stock_disponible + ? WHERE id = ?",
			item.cantidad, item.sectorID,
		); err != nil {
			return false, fmt.Errorf("error restoring stock: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("error committing release: %w", err)
	}
	return true, nil
}

// GetOrderDetails trae la orden junto con el nombre del evento, el email del comprador y sus
// ítems (sector y precio congelado). LEFT JOIN: la orden se sigue encontrando si el
// organizador eliminó el evento o el sector.
func (r *Repository) GetOrderDetails(ctx context.Context, orderID int64) (*domain.Order, error) {
	var o domain.Order
	err := r.db.QueryRowContext(
		ctx,
		`SELECT o.id, o.evento_id, o.usuario_id, COALESCE(u.email, ''), COALESCE(e.nombre, 'Evento eliminado'), o.estado,
		        TRIM(CONCAT(COALESCE(u.nombre, ''), ' ', COALESCE(u.apellido, ''))), COALESCE(u.numero_dni, '')
		 FROM ordenes o
		 LEFT JOIN eventos e ON e.id = o.evento_id
		 LEFT JOIN usuarios u ON u.id = o.usuario_id
		 WHERE o.id = ?`,
		orderID,
	).Scan(&o.ID, &o.EventID, &o.UserID, &o.UserEmail, &o.EventName, &o.Status, &o.BuyerName, &o.BuyerDNI)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrOrderNotFound
		}
		return nil, fmt.Errorf("error fetching order: %w", err)
	}

	rows, err := r.db.QueryContext(
		ctx,
		`SELECT COALESCE(i.sector_id, 0), COALESCE(s.nombre, ''), i.cantidad, COALESCE(i.precio_unitario, s.precio, 0)
		 FROM orden_items i
		 LEFT JOIN sectores s ON s.id = i.sector_id
		 WHERE i.orden_id = ?
		 ORDER BY i.id`,
		orderID,
	)
	if err != nil {
		return nil, fmt.Errorf("error fetching order items: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var item domain.OrderItem
		if err := rows.Scan(&item.SectorID, &item.SectorName, &item.Quantity, &item.UnitPrice); err != nil {
			return nil, err
		}
		o.Items = append(o.Items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return &o, nil
}

// GetExpiredReservations lista reservas vencidas según MySQL. Es la red de seguridad
// del expirador por si Redis perdió la clave (reinicio, flush, etc.).
func (r *Repository) GetExpiredReservations(ctx context.Context, limit int) ([]int64, error) {
	rows, err := r.db.QueryContext(
		ctx,
		`SELECT id FROM ordenes
		 WHERE estado = 'reservada' AND reservada_hasta < NOW()
		 ORDER BY reservada_hasta LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("error listing expired reservations: %w", err)
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
