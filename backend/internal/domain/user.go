package domain

import (
	"errors"
	"fmt"
	"time"
)

var (
	ErrUserNotFound = errors.New("user not found")
	ErrEmailTaken   = errors.New("email already registered")
	// ErrCustomerAccount: el panel solo gestiona al equipo (admins y organizadores).
	ErrCustomerAccount = errors.New("customer accounts cannot be managed from the panel")
)

// UserHasOrdersError impide borrar a un usuario con compras: sus órdenes y entradas lo referencian.
type UserHasOrdersError struct {
	Orders int
}

func (e *UserHasOrdersError) Error() string {
	return fmt.Sprintf("user has %d orders", e.Orders)
}

// NewUser es un alta desde el backoffice. PasswordHash ya viene hasheado (bcrypt).
type NewUser struct {
	Email         string
	PasswordHash  string
	Nombre        string
	Apellido      string
	Rol           string
	OrganizadorID int64 // Solo para RolOrganizador
}

// UserSummary es un usuario tal como lo ve el admin en la gestión de roles.
type UserSummary struct {
	ID                int64     `json:"id"`
	Email             string    `json:"email"`
	Nombre            string    `json:"nombre"`
	Apellido          string    `json:"apellido"`
	Rol               string    `json:"rol"`
	OrganizadorID     int64     `json:"organizador_id"`
	OrganizadorNombre string    `json:"organizador_nombre"`
	CreadoEn          time.Time `json:"creado_en"`
}

// Tipos de documento aceptados en el registro.
const (
	DocumentDNI       = "DNI"
	DocumentPassport  = "PASAPORTE"
	DocumentCedula    = "CI"
	DocumentOtherType = "OTRO"
)

// Roles de usuario (columna usuarios.rol). El registro público siempre crea RolUsuario:
// el rol nunca se toma del request.
const (
	RolUsuario     = "usuario"
	RolOrganizador = "organizador"
	RolAdmin       = "admin"
	RolPortero     = "portero" // Solo escanea entradas de los eventos de su productora
)

// RolDeProductora indica si el rol opera sobre una productora (usuarios.organizador_id).
func RolDeProductora(rol string) bool {
	return rol == RolOrganizador || rol == RolPortero
}

// RolDeEquipo indica si el rol es del equipo (lo gestiona el panel) y no un cliente.
func RolDeEquipo(rol string) bool {
	return rol == RolAdmin || RolDeProductora(rol)
}

// User es la fila de la tabla usuarios (sin el hash de la contraseña).
type User struct {
	ID                int64     `json:"id"`
	Email             string    `json:"email"`
	Nombre            string    `json:"nombre"`
	Apellido          string    `json:"apellido"`
	PaisResidencia    string    `json:"pais_residencia"`
	Telefono          string    `json:"telefono"`
	TipoDocumento     string    `json:"tipo_documento"`
	NumeroDNI         string    `json:"numero_dni"`
	FechaNacimiento   time.Time `json:"fecha_nacimiento"`
	AceptaSuscripcion bool      `json:"acepta_suscripcion"`
	Rol               string    `json:"rol"`
	OrganizadorID     int64     `json:"organizador_id,omitempty"` // Solo para RolOrganizador
}
