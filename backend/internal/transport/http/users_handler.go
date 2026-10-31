package http

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"

	"stock-api/internal/domain"
	"stock-api/internal/platform/auth"
)

// userAdminStore es lo que la gestión de roles necesita del repositorio.
// scope es el de organizadorScope: 0 para el admin, la productora para un organizador.
type userAdminStore interface {
	ListUsers(ctx context.Context, q string, scope int64) ([]domain.UserSummary, int, error)
	GetUserSummary(ctx context.Context, userID int64) (*domain.UserSummary, error)
	CreateUser(ctx context.Context, u domain.NewUser) (int64, error)
	UpdateUserProfile(ctx context.Context, userID, scope int64, nombre, apellido, email string) error
	UpdateUserRole(ctx context.Context, userID int64, rol string, organizadorID, scope int64) error
	DeleteUser(ctx context.Context, userID, scope int64) error
}

// roleSource lee el rol vigente de un usuario.
type roleSource interface {
	GetUserRole(ctx context.Context, userID int64) (string, int64, error)
}

// CurrentRole reemplaza el rol y la productora del JWT por los de la base. El JWT dura 24 h:
// sin esto, a un admin que se degrada (o a un organizador al que se le cambia la productora)
// le seguirían valiendo los permisos viejos hasta que venza su token.
// Va después de AuthMiddleware y antes de RequireRole.
func CurrentRole(users roleSource) gin.HandlerFunc {
	return func(c *gin.Context) {
		rol, organizadorID, err := users.GetUserRole(c.Request.Context(), c.GetInt64(auth.UserIDContextKey))
		if errors.Is(err, domain.ErrUserNotFound) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired token"})
			return
		}
		if err != nil {
			log.Printf("Error leyendo el rol del usuario %d: %v", c.GetInt64(auth.UserIDContextKey), err)
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.Set(auth.RolContextKey, rol)
		c.Set(auth.OrganizadorIDContextKey, organizadorID)
		c.Next()
	}
}

// UsersHandler atiende /api/admin/usuarios: el admin gestiona a todo el equipo; un organizador,
// solo a los organizadores y porteros de su productora.
type UsersHandler struct {
	repo userAdminStore
}

func NewUsersHandler(repo userAdminStore) *UsersHandler {
	return &UsersHandler{repo: repo}
}

// ListUsers: GET /api/admin/usuarios?q=texto
func (h *UsersHandler) ListUsers(c *gin.Context) {
	scope, ok := organizadorScope(c)
	if !ok {
		return
	}
	users, total, err := h.repo.ListUsers(c.Request.Context(), c.Query("q"), scope)
	if err != nil {
		log.Printf("Error listing users: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "No se pudieron cargar los usuarios"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"usuarios": users, "total": total})
}

type updateRoleRequest struct {
	Rol           string `json:"rol"`
	OrganizadorID int64  `json:"organizador_id"` // Obligatorio para organizador y portero
}

// validateRole controla el rol y que los roles de productora traigan la suya. Desde el panel
// solo se asignan roles del equipo: los clientes se registran solos y no se gestionan acá.
func validateRole(rol string, organizadorID int64) *fieldError {
	switch {
	case rol == domain.RolAdmin:
		return nil
	case domain.RolDeProductora(rol):
		if organizadorID <= 0 {
			return &fieldError{"organizador_id", "Elegí la productora"}
		}
		return nil
	}
	return &fieldError{"rol", "Elegí un rol del equipo: administrador, organizador o portero"}
}

// roleForScope aplica los límites de un organizador (scope > 0) al rol que quiere asignar:
// solo organizador o portero, y siempre de su propia productora (se completa sola si no
// viene). Devuelve la productora a guardar; si rechaza ya deja escrita la respuesta (403).
func roleForScope(c *gin.Context, scope int64, rol string, organizadorID int64) (int64, bool) {
	if scope == 0 {
		return organizadorID, true
	}
	if !domain.RolDeProductora(rol) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Solo podés asignar los roles organizador o portero", "field": "rol"})
		return 0, false
	}
	if organizadorID != 0 && organizadorID != scope {
		c.JSON(http.StatusForbidden, gin.H{"error": "No podés asignar usuarios a otra productora", "field": "organizador_id"})
		return 0, false
	}
	return scope, true
}

// customerAccountForbidden es la respuesta a cualquier cambio sobre la cuenta de un cliente.
func customerAccountForbidden(c *gin.Context) {
	c.JSON(http.StatusForbidden, gin.H{"error": "No se pueden modificar cuentas de clientes desde este panel"})
}

// userIDParam lee el :id de la ruta; si es inválido ya deja escrita la respuesta.
func userIDParam(c *gin.Context) (int64, bool) {
	userID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || userID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ID de usuario inválido"})
		return 0, false
	}
	return userID, true
}

// profileFields normaliza y valida nombre, apellido (opcional) y email.
func profileFields(nombre, apellido, email string) (string, string, string, *fieldError) {
	nombre, err := requiredText("nombre", "El nombre", nombre, 100)
	if err != nil {
		return "", "", "", err.(*fieldError)
	}
	apellido = strings.Join(strings.Fields(apellido), " ")
	if len([]rune(apellido)) > 100 {
		return "", "", "", &fieldError{"apellido", "El apellido es demasiado largo"}
	}
	email = strings.ToLower(strings.TrimSpace(email))
	if !validEmail(email) {
		return "", "", "", &fieldError{"email", "Ingresá un correo electrónico válido"}
	}
	return nombre, apellido, email, nil
}

func (h *UsersHandler) respondWithUser(c *gin.Context, userID int64, status int) {
	user, err := h.repo.GetUserSummary(c.Request.Context(), userID)
	if err != nil {
		log.Printf("Error fetching user %d after admin change: %v", userID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "El usuario se guardó pero no se pudo leer"})
		return
	}
	c.JSON(status, user)
}

type createUserRequest struct {
	Nombre        string `json:"nombre"`
	Apellido      string `json:"apellido"`
	Email         string `json:"email"`
	Password      string `json:"password"`
	Rol           string `json:"rol"`
	OrganizadorID int64  `json:"organizador_id"`
}

// CreateUser: POST /api/admin/usuarios {nombre, apellido, email, password, rol, organizador_id}
// Misma política de contraseñas que el registro; se guarda solo el hash (bcrypt).
func (h *UsersHandler) CreateUser(c *gin.Context) {
	var req createUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Datos inválidos"})
		return
	}
	scope, ok := organizadorScope(c)
	if !ok {
		return
	}
	if req.OrganizadorID, ok = roleForScope(c, scope, req.Rol, req.OrganizadorID); !ok {
		return
	}
	nombre, apellido, email, ferr := profileFields(req.Nombre, req.Apellido, req.Email)
	if ferr == nil {
		if err := validatePassword(req.Password); err != nil {
			ferr = err.(*fieldError)
		}
	}
	if ferr == nil {
		ferr = validateRole(req.Rol, req.OrganizadorID)
	}
	if ferr != nil {
		ferr.respond(c)
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		log.Printf("Error hashing admin-created password: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "No se pudo crear el usuario"})
		return
	}
	userID, err := h.repo.CreateUser(c.Request.Context(), domain.NewUser{
		Email: email, PasswordHash: string(hash), Nombre: nombre, Apellido: apellido,
		Rol: req.Rol, OrganizadorID: req.OrganizadorID,
	})
	switch {
	case err == nil:
		h.respondWithUser(c, userID, http.StatusCreated)
	case errors.Is(err, domain.ErrEmailTaken):
		c.JSON(http.StatusConflict, gin.H{"error": "Ya existe una cuenta con ese correo electrónico", "field": "email"})
	case errors.Is(err, domain.ErrOrganizadorNotFound):
		c.JSON(http.StatusBadRequest, gin.H{"error": "La productora no existe", "field": "organizador_id"})
	default:
		log.Printf("Error creating user from admin: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "No se pudo crear el usuario"})
	}
}

type updateUserRequest struct {
	Nombre   string `json:"nombre"`
	Apellido string `json:"apellido"`
	Email    string `json:"email"`
}

// UpdateUser: PUT /api/admin/usuarios/:id {nombre, apellido, email}
func (h *UsersHandler) UpdateUser(c *gin.Context) {
	userID, ok := userIDParam(c)
	if !ok {
		return
	}
	var req updateUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Datos inválidos"})
		return
	}
	nombre, apellido, email, ferr := profileFields(req.Nombre, req.Apellido, req.Email)
	if ferr != nil {
		ferr.respond(c)
		return
	}
	scope, ok := organizadorScope(c)
	if !ok {
		return
	}

	switch err := h.repo.UpdateUserProfile(c.Request.Context(), userID, scope, nombre, apellido, email); {
	case err == nil:
		h.respondWithUser(c, userID, http.StatusOK)
	case errors.Is(err, domain.ErrUserNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "Usuario no encontrado"})
	case errors.Is(err, domain.ErrCustomerAccount):
		customerAccountForbidden(c)
	case errors.Is(err, domain.ErrEmailTaken):
		c.JSON(http.StatusConflict, gin.H{"error": "Ya existe otra cuenta con ese correo electrónico", "field": "email"})
	default:
		log.Printf("Error updating user %d: %v", userID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "No se pudo actualizar el usuario"})
	}
}

// DeleteUser: DELETE /api/admin/usuarios/:id
// Solo usuarios sin órdenes: las compras y entradas nominativas guardan quién compró.
// Para quitarle el acceso al panel a alguien con compras se le cambia el rol.
func (h *UsersHandler) DeleteUser(c *gin.Context) {
	userID, ok := userIDParam(c)
	if !ok {
		return
	}
	if userID == c.GetInt64(auth.UserIDContextKey) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No podés eliminar tu propio usuario"})
		return
	}

	scope, ok := organizadorScope(c)
	if !ok {
		return
	}
	err := h.repo.DeleteUser(c.Request.Context(), userID, scope)
	var hasOrders *domain.UserHasOrdersError
	switch {
	case err == nil:
		c.Status(http.StatusNoContent)
	case errors.Is(err, domain.ErrUserNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "Usuario no encontrado"})
	case errors.Is(err, domain.ErrCustomerAccount):
		customerAccountForbidden(c)
	case errors.As(err, &hasOrders):
		c.JSON(http.StatusConflict, gin.H{
			"error":   fmt.Sprintf("No se puede eliminar: tiene %d %s y sus entradas lo referencian. Podés quitarle el acceso cambiándole el rol", hasOrders.Orders, plural(hasOrders.Orders, "compra asociada", "compras asociadas")),
			"ordenes": hasOrders.Orders,
		})
	default:
		log.Printf("Error deleting user %d: %v", userID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "No se pudo eliminar el usuario"})
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// UpdateRole: PUT /api/admin/usuarios/:id/rol {rol, organizador_id}
func (h *UsersHandler) UpdateRole(c *gin.Context) {
	userID, ok := userIDParam(c)
	if !ok {
		return
	}
	var req updateRoleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Datos inválidos"})
		return
	}
	scope, ok := organizadorScope(c)
	if !ok {
		return
	}
	if req.OrganizadorID, ok = roleForScope(c, scope, req.Rol, req.OrganizadorID); !ok {
		return
	}
	if ferr := validateRole(req.Rol, req.OrganizadorID); ferr != nil {
		ferr.respond(c)
		return
	}
	// Evita que el último admin se quite el acceso por error: otro admin puede hacerlo.
	if userID == c.GetInt64(auth.UserIDContextKey) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No podés cambiar tu propio rol"})
		return
	}

	switch err := h.repo.UpdateUserRole(c.Request.Context(), userID, req.Rol, req.OrganizadorID, scope); {
	case err == nil:
		c.JSON(http.StatusOK, gin.H{"id": userID, "rol": req.Rol})
	case errors.Is(err, domain.ErrUserNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "Usuario no encontrado"})
	case errors.Is(err, domain.ErrCustomerAccount):
		customerAccountForbidden(c)
	case errors.Is(err, domain.ErrOrganizadorNotFound):
		c.JSON(http.StatusBadRequest, gin.H{"error": "La productora no existe", "field": "organizador_id"})
	default:
		log.Printf("Error updating role of user %d: %v", userID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "No se pudo cambiar el rol"})
	}
}
