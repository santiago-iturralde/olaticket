package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"stock-api/internal/domain"
)

// Repository implementa la interfaz domain.EventRepository para MySQL.
type Repository struct {
	db *sql.DB
}

// NewRepository crea una nueva instancia del repositorio.
func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

// GetByID busca un evento por su ID en la tabla 'eventos'.
func (r *Repository) GetByID(ctx context.Context, id int64) (*domain.Event, error) {
	query := "SELECT id, nombre, total_tickets, stock_disponible FROM eventos WHERE id = ?"
	row := r.db.QueryRowContext(ctx, query, id)

	var e domain.Event
	err := row.Scan(&e.ID, &e.Nombre, &e.TotalTickets, &e.StockDisponible)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, domain.ErrEventNotFound
		}
		return nil, fmt.Errorf("error fetching event: %w", err)
	}
	return &e, nil
}

// DecreaseStock reduce el stock de forma segura usando SQL atómico.
func (r *Repository) DecreaseStock(ctx context.Context, id int64, quantity int) error {
	// UPDATE atómico: solo actualiza si stock_disponible >= cantidad
	query := "UPDATE eventos SET stock_disponible = stock_disponible - ? WHERE id = ? AND stock_disponible >= ?"

	result, err := r.db.ExecContext(ctx, query, quantity, id, quantity)
	if err != nil {
		return fmt.Errorf("error decreasing stock: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("error checking affected rows: %w", err)
	}

	if rowsAffected == 0 {
		return domain.ErrNotEnoughStock
	}

	return nil
}

// CreateOrder guarda la orden inicial con estado 'pendiente' y retorna el ID generado.
func (r *Repository) CreateOrder(ctx context.Context, eventID int64, userID int) (int64, error) {
	query := "INSERT INTO ordenes (evento_id, usuario_id, estado) VALUES (?, ?, 'pendiente')"

	res, err := r.db.ExecContext(ctx, query, eventID, userID)
	if err != nil {
		return 0, fmt.Errorf("error creating order: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("error getting last insert id: %w", err)
	}

	return id, nil
}

// UpdateOrderStatus actualiza el estado final (confirmada/rechazada)
func (r *Repository) UpdateOrderStatus(ctx context.Context, orderID int64, status string) error {
	query := "UPDATE ordenes SET estado = ? WHERE id = ?"
	_, err := r.db.ExecContext(ctx, query, status, orderID)
	if err != nil {
		return fmt.Errorf("error updating order status: %w", err)
	}
	return nil
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

// GetAllEvents devuelve la lista de todos los eventos disponibles
func (r *Repository) GetAllEvents(ctx context.Context) ([]domain.Event, error) {
	query := "SELECT id, nombre, total_tickets, stock_disponible FROM eventos"
	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var eventos []domain.Event
	for rows.Next() {
		var e domain.Event
		if err := rows.Scan(&e.ID, &e.Nombre, &e.TotalTickets, &e.StockDisponible); err != nil {
			return nil, err
		}
		eventos = append(eventos, e)
	}
	return eventos, nil
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
