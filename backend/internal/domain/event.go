package domain

import (
	"context"
	"errors"
)

//errores de dominio
//Los defino aca para que los servicios los usen

var (
	ErrEventNotFound  = errors.New("event not found")
	ErrNotEnoughStock = errors.New("not enough stock available")
)

// Event representa un evento con entradas a la venta.
// Uso tags `json:"..."` para que cuando esto se envíe por API, se vea bien en minúsculas.

type Event struct {
	ID              int64  `json:"id"`
	Nombre          string `json:"nombre"`
	TotalTickets    int    `json:"total_tickets"`
	StockDisponible int    `json:"StockDisponible"` // Dato crítico para la concurrencia
}

// EventRepository define el contrato para interactuar con la base de datos.
// Es una interfaz. No dice "MySQL" ni "Postgres". Solo dice "Necesito guardar y leer".
// El contexto (ctx) es obligatorio en Go moderno para manejar timeouts y cancelaciones.

type EventRepository interface {
	GetByID(ctx context.Context, id int64) (*Event, error)
	DecreaseStock(ctx context.Context, id int64, quantity int) error
	CreateOrder(ctx context.Context, eventID int64, userID int) (int64, error)
	UpdateOrderStatus(ctx context.Context, orderID int64, status string) error
	GetOrder(ctx context.Context, orderID int64) (string, error)
	GetAllEvents(ctx context.Context) ([]Event, error)
	GetQueuePosition(ctx context.Context, orderID int64, eventID int64) (int, error)
}
