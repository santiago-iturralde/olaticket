package http

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"stock-api/internal/domain"
)

// Columnas organizadores.nombre VARCHAR(150), email_contacto VARCHAR(255), telefono VARCHAR(30).
const (
	maxProductoraNameLength  = 150
	maxProductoraEmailLength = 255
	maxProductoraPhoneLength = 30
)

// productoraStore es lo que el CRUD de productoras necesita del repositorio.
type productoraStore interface {
	ListProductoras(ctx context.Context) ([]domain.ProductoraSummary, error)
	GetProductora(ctx context.Context, id int64) (*domain.ProductoraSummary, error)
	CreateProductora(ctx context.Context, p domain.Organizador) (int64, error)
	UpdateProductora(ctx context.Context, p domain.Organizador) error
	DeleteProductora(ctx context.Context, id int64) error
}

// ProductorasHandler atiende /api/admin/productoras (solo RolAdmin). Las productoras son los
// tenants: la tabla es organizadores y los usuarios/eventos la referencian por organizador_id.
type ProductorasHandler struct {
	repo productoraStore
}

func NewProductorasHandler(repo productoraStore) *ProductorasHandler {
	return &ProductorasHandler{repo: repo}
}

type productoraRequest struct {
	Nombre        string `json:"nombre"`
	EmailContacto string `json:"email_contacto"`
	Telefono      string `json:"telefono"`
}

func (req productoraRequest) toOrganizador() (domain.Organizador, *fieldError) {
	p := domain.Organizador{
		Nombre:        strings.Join(strings.Fields(req.Nombre), " "),
		EmailContacto: strings.ToLower(strings.TrimSpace(req.EmailContacto)),
		Telefono:      strings.TrimSpace(req.Telefono),
	}
	switch {
	case p.Nombre == "" || utf8.RuneCountInString(p.Nombre) > maxProductoraNameLength:
		return p, &fieldError{"nombre", fmt.Sprintf("El nombre es obligatorio (máximo %d caracteres)", maxProductoraNameLength)}
	case len(p.EmailContacto) > maxProductoraEmailLength || !validEmail(p.EmailContacto):
		return p, &fieldError{"email_contacto", "Ingresá un correo de contacto válido"}
	case utf8.RuneCountInString(p.Telefono) > maxProductoraPhoneLength:
		return p, &fieldError{"telefono", fmt.Sprintf("Máximo %d caracteres", maxProductoraPhoneLength)}
	}
	return p, nil
}

func productoraIDParam(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ID de productora inválido"})
		return 0, false
	}
	return id, true
}

func (h *ProductorasHandler) respondWithProductora(c *gin.Context, id int64, status int) {
	p, err := h.repo.GetProductora(c.Request.Context(), id)
	if err != nil {
		log.Printf("Error fetching productora %d after admin change: %v", id, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "La productora se guardó pero no se pudo leer"})
		return
	}
	c.JSON(status, p)
}

// List: GET /api/admin/productoras
func (h *ProductorasHandler) List(c *gin.Context) {
	productoras, err := h.repo.ListProductoras(c.Request.Context())
	if err != nil {
		log.Printf("Error listing productoras: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "No se pudieron cargar las productoras"})
		return
	}
	c.JSON(http.StatusOK, productoras)
}

// Create: POST /api/admin/productoras {nombre, email_contacto, telefono}
func (h *ProductorasHandler) Create(c *gin.Context) {
	var req productoraRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Datos inválidos"})
		return
	}
	p, ferr := req.toOrganizador()
	if ferr != nil {
		ferr.respond(c)
		return
	}
	id, err := h.repo.CreateProductora(c.Request.Context(), p)
	if err != nil {
		log.Printf("Error creating productora: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "No se pudo crear la productora"})
		return
	}
	h.respondWithProductora(c, id, http.StatusCreated)
}

// Update: PUT /api/admin/productoras/:id {nombre, email_contacto, telefono}
func (h *ProductorasHandler) Update(c *gin.Context) {
	id, ok := productoraIDParam(c)
	if !ok {
		return
	}
	var req productoraRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Datos inválidos"})
		return
	}
	p, ferr := req.toOrganizador()
	if ferr != nil {
		ferr.respond(c)
		return
	}
	p.ID = id
	switch err := h.repo.UpdateProductora(c.Request.Context(), p); {
	case err == nil:
		h.respondWithProductora(c, id, http.StatusOK)
	case errors.Is(err, domain.ErrOrganizadorNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "Productora no encontrada"})
	default:
		log.Printf("Error updating productora %d: %v", id, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "No se pudo actualizar la productora"})
	}
}

// Delete: DELETE /api/admin/productoras/:id
// Solo productoras sin eventos ni usuarios: hay que eliminar o mover lo suyo primero.
func (h *ProductorasHandler) Delete(c *gin.Context) {
	id, ok := productoraIDParam(c)
	if !ok {
		return
	}
	err := h.repo.DeleteProductora(c.Request.Context(), id)
	var inUse *domain.ProductoraInUseError
	switch {
	case err == nil:
		c.Status(http.StatusNoContent)
	case errors.Is(err, domain.ErrOrganizadorNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "Productora no encontrada"})
	case errors.As(err, &inUse):
		var partes []string
		if inUse.Eventos > 0 {
			partes = append(partes, fmt.Sprintf("%d %s", inUse.Eventos, plural(inUse.Eventos, "evento", "eventos")))
		}
		if inUse.Usuarios > 0 {
			partes = append(partes, fmt.Sprintf("%d %s", inUse.Usuarios, plural(inUse.Usuarios, "usuario", "usuarios")))
		}
		c.JSON(http.StatusConflict, gin.H{
			"error":    "No se puede eliminar: tiene " + strings.Join(partes, " y ") + ". Eliminalos o pasalos a otra productora primero",
			"eventos":  inUse.Eventos,
			"usuarios": inUse.Usuarios,
		})
	default:
		log.Printf("Error deleting productora %d: %v", id, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "No se pudo eliminar la productora"})
	}
}
