package domain

import (
	"fmt"
	"time"
)

// Organizador es la productora/teatro dueña de los eventos (el tenant del SaaS).
type Organizador struct {
	ID            int64     `json:"id"`
	Nombre        string    `json:"nombre"`
	EmailContacto string    `json:"email_contacto"`
	Telefono      string    `json:"telefono"`
	CreadoEn      time.Time `json:"creado_en,omitzero"` // No se carga al embeberlo en un evento
}

// ProductoraSummary es una productora con lo que depende de ella (para el admin).
type ProductoraSummary struct {
	Organizador
	Eventos  int `json:"eventos"`
	Usuarios int `json:"usuarios"` // Organizadores y porteros
}

// ProductoraInUseError impide borrar una productora con eventos o usuarios: los eventos
// tienen FK RESTRICT y sus usuarios quedarían sin tenant (sin acceso a nada).
type ProductoraInUseError struct {
	Eventos  int
	Usuarios int
}

func (e *ProductoraInUseError) Error() string {
	return fmt.Sprintf("productora has %d events and %d users", e.Eventos, e.Usuarios)
}
