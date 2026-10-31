package domain

import (
	"context"
	"errors"
	"fmt"
	"time"
)

//errores de dominio
//Los defino aca para que los servicios los usen

var (
	ErrEventNotFound  = errors.New("event not found")
	ErrNotEnoughStock = errors.New("not enough stock available")
	ErrOrderNotFound  = errors.New("order not found")

	ErrOrganizadorNotFound = errors.New("organizador not found")
	ErrTotalBelowSold      = errors.New("total tickets below tickets already sold or reserved")
	ErrEventHasSales       = errors.New("event has sold tickets")

	ErrSectorNotFound    = errors.New("sector not found")
	ErrSectorHasSales    = errors.New("sector has sold or reserved tickets")
	ErrOverPurchaseLimit = errors.New("quantity exceeds the per-order limit")

	// Control de acceso (escáner)
	ErrTicketNotFound    = errors.New("ticket not found")
	ErrTicketOtherEvent  = errors.New("ticket belongs to another event")
	ErrTicketNotPaid     = errors.New("ticket order is not confirmed")
	ErrTicketAlreadyUsed = errors.New("ticket already used")
)

// DefaultLimiteTicketsPorOrden es el tope de entradas por compra cuando el organizador no
// indica otro: alcanza para un grupo chico y le complica la vida a la reventa.
const DefaultLimiteTicketsPorOrden = 4

// SectorError ubica un error de sector en la lista que mandó el backoffice.
// Index es la posición en EventUpdate.Sectores, o -1 si es un sector existente que se quitó.
type SectorError struct {
	Index  int
	Nombre string
	Err    error
}

func (e *SectorError) Error() string {
	return fmt.Sprintf("sector %q: %v", e.Nombre, e.Err)
}

func (e *SectorError) Unwrap() error { return e.Err }

// Estados posibles de una orden.
// pendiente -> reservada -> confirmada | rechazada | expirada
const (
	OrderPending   = "pendiente"
	OrderReserved  = "reservada"
	OrderConfirmed = "confirmada"
	OrderRejected  = "rechazada"
	OrderExpired   = "expirada"
)

// Event representa un evento con entradas a la venta.
// Uso tags `json:"..."` para que cuando esto se envíe por API, se vea bien en minúsculas.

type Event struct {
	ID                    int64    `json:"id"`
	Nombre                string   `json:"nombre"`
	LimiteTicketsPorOrden int      `json:"limite_tickets_por_orden"`
	Sectores              []Sector `json:"sectores"`

	// URLs públicas (Cloudinary); "" si el evento no tiene imagen.
	PortadaURL string `json:"portada_url"`
	MapaURL    string `json:"mapa_url"`

	// Sumas de los sectores (solo lectura): las calcula SetSectores.
	TotalTickets    int `json:"total_tickets"`
	StockDisponible int `json:"StockDisponible"`

	OrganizadorID int64       `json:"organizador_id"`
	Organizador   Organizador `json:"organizador"` // Datos básicos, cargados con JOIN
}

// Sector es una zona del evento (General, Platea VIP, ...) con su propio precio y cupo.
type Sector struct {
	ID              int64   `json:"id"`
	EventoID        int64   `json:"evento_id"`
	Nombre          string  `json:"nombre"`
	Precio          float64 `json:"precio"`
	Capacidad       int     `json:"capacidad"`
	StockDisponible int     `json:"stock_disponible"` // Dato crítico para la concurrencia
}

// SetSectores asigna los sectores y recalcula los totales del evento.
func (e *Event) SetSectores(sectores []Sector) {
	if sectores == nil {
		sectores = []Sector{} // [] y no null para el frontend
	}
	e.Sectores = sectores
	e.TotalTickets, e.StockDisponible = 0, 0
	for _, s := range sectores {
		e.TotalTickets += s.Capacidad
		e.StockDisponible += s.StockDisponible
	}
}

// Sector busca un sector del evento por su ID.
func (e *Event) Sector(id int64) (*Sector, bool) {
	for i := range e.Sectores {
		if e.Sectores[i].ID == id {
			return &e.Sectores[i], true
		}
	}
	return nil, false
}

// EventSummary es un evento con sus métricas de venta para el backoffice.
// Solo cuentan las órdenes 'confirmada' (pagadas), al precio congelado en cada orden.
type EventSummary struct {
	Event
	EntradasVendidas int     `json:"entradas_vendidas"`
	Recaudacion      float64 `json:"recaudacion"`
}

// EventUpdate son los cambios del backoffice sobre un evento. nil = no se modifica.
// Sectores, si viene, es la lista completa: los que tienen ID se editan, los que no se crean
// y los existentes que no aparecen se eliminan.
// PortadaURL y MapaURL: "" quita la imagen.
type EventUpdate struct {
	Nombre                *string
	LimiteTicketsPorOrden *int
	Sectores              []SectorInput
	PortadaURL            *string
	MapaURL               *string
}

// SectorInput es un sector tal como llega del backoffice (ID 0 = sector nuevo).
type SectorInput struct {
	ID        int64
	Nombre    string
	Precio    float64
	Capacidad int
}

// OrderItemInput es una línea del carrito: cuántas entradas de qué sector.
type OrderItemInput struct {
	SectorID int64
	Cantidad int
}

// Order es la vista completa de una orden que necesita el checkout.
type Order struct {
	ID        int64
	EventID   int64
	UserID    int64
	UserEmail string
	EventName string
	Items     []OrderItem
	Status    string

	// Datos del comprador: el checkout los propone para la primera entrada.
	BuyerName string
	BuyerDNI  string
}

// Asistente es la persona que va a usar una entrada: lo carga el comprador en el checkout.
type Asistente struct {
	SectorID int64  `json:"sector_id"`
	Nombre   string `json:"nombre"`
	DNI      string `json:"dni"`
}

// Ticket es una entrada nominativa: se crea al confirmarse el pago, una por persona.
type Ticket struct {
	ID          int64      `json:"id"`
	OrderID     int64      `json:"orden_id"`
	SectorID    int64      `json:"sector_id"`
	SectorName  string     `json:"sector"`
	Nombre      string     `json:"nombre"`
	DNI         string     `json:"dni"`
	Ingresado   bool       `json:"ingresado"`
	IngresadoEn *time.Time `json:"ingresado_en,omitempty"`
}

// ScanResult es lo que ve la puerta al leer un QR. Ticket viene cargado también cuando
// el acceso se rechaza por ya usado o impago, para mostrar a quién pertenece.
type ScanResult struct {
	Ticket
	EventID     int64
	OrderStatus string
}

// OrderItem son las entradas de un sector dentro de la orden, al precio congelado al reservar.
type OrderItem struct {
	SectorID   int64
	SectorName string
	Quantity   int
	UnitPrice  float64
}

func (i OrderItem) Subtotal() float64 {
	return i.UnitPrice * float64(i.Quantity)
}

// Title es el concepto que se cobra: "Evento - Sector".
func (i OrderItem) Title(eventName string) string {
	if i.SectorName == "" {
		return eventName
	}
	return eventName + " - " + i.SectorName
}

// Total es la suma de los ítems.
func (o *Order) Total() float64 {
	var total float64
	for _, item := range o.Items {
		total += item.Subtotal()
	}
	return total
}

// Quantity es la cantidad total de entradas de la orden.
func (o *Order) Quantity() int {
	n := 0
	for _, item := range o.Items {
		n += item.Quantity
	}
	return n
}

type OrderStatus struct {
	Status  string
	QRToken string
	UserID  int64
	EventID int64
}

// EventRepository define el contrato para interactuar con la base de datos.
// Es una interfaz. No dice "MySQL" ni "Postgres". Solo dice "Necesito guardar y leer".
// El contexto (ctx) es obligatorio en Go moderno para manejar timeouts y cancelaciones.

type EventRepository interface {
	GetByID(ctx context.Context, id int64) (*Event, error)
	CreateOrder(ctx context.Context, eventID, userID int64, items []OrderItemInput) (int64, error)
	UpdateOrderStatus(ctx context.Context, orderID int64, status, qrToken string) error
	GetOrder(ctx context.Context, orderID int64) (string, error)
	GetOrderStatus(ctx context.Context, orderID int64) (*OrderStatus, error)
	GetAllEvents(ctx context.Context) ([]Event, error)
	GetQueuePosition(ctx context.Context, orderID int64, eventID int64) (int, error)

	// Reserva temporal y pagos
	ReserveStock(ctx context.Context, orderID, eventID int64, ttlSeconds int) error
	ReleaseReservation(ctx context.Context, orderID int64, status string) (bool, error)
	ConfirmOrder(ctx context.Context, orderID int64, paymentID, qrToken string) (bool, error)
	GetOrderDetails(ctx context.Context, orderID int64) (*Order, error)
	GetExpiredReservations(ctx context.Context, limit int) ([]int64, error)

	// Entradas nominativas
	SetAttendees(ctx context.Context, orderID int64, asistentes []Asistente) (bool, error)
	GetOrderTickets(ctx context.Context, orderID int64) ([]Ticket, error)
}
