package http

import (
	"database/sql"
	"errors"
	"log"
	"net/http"
	"net/mail"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	mysqlDriver "github.com/go-sql-driver/mysql"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"

	"stock-api/internal/domain"
	"stock-api/internal/platform/auth"
	"stock-api/internal/platform/email"
	"stock-api/internal/platform/recaptcha"
)

type AuthHandler struct {
	db          *sql.DB
	rdb         *redis.Client
	captcha     *recaptcha.Verifier
	mailer      *email.Sender
	frontendURL string
}

func NewAuthHandler(
	db *sql.DB,
	rdb *redis.Client,
	captcha *recaptcha.Verifier,
	mailer *email.Sender,
	frontendURL string,
) *AuthHandler {
	return &AuthHandler{db: db, rdb: rdb, captcha: captcha, mailer: mailer, frontendURL: frontendURL}
}

type loginRequest struct {
	Email          string `json:"email" binding:"required"`
	Password       string `json:"password" binding:"required"`
	RecaptchaToken string `json:"recaptcha_token"`
}

type registerRequest struct {
	Email             string `json:"email"`
	Password          string `json:"password"`
	Nombre            string `json:"nombre"`
	Apellido          string `json:"apellido"`
	PaisResidencia    string `json:"pais_residencia"`
	Telefono          string `json:"telefono"`
	TipoDocumento     string `json:"tipo_documento"`
	NumeroDNI         string `json:"numero_dni"`
	FechaNacimiento   string `json:"fecha_nacimiento"` // YYYY-MM-DD
	AceptaSuscripcion bool   `json:"acepta_suscripcion"`
	RecaptchaToken    string `json:"recaptcha_token"`
}

// Nombre del índice UNIQUE de la migración 005 (tipo_documento, numero_dni).
const documentUniqueIndex = "uq_usuarios_documento"

// fieldError es un error de validación asociado a un campo del formulario.
type fieldError struct {
	Field   string
	Message string
}

func (e *fieldError) Error() string { return e.Message }

var (
	phonePattern      = regexp.MustCompile(`^\+\d{1,4}( ?\d){6,15}$`)
	dniPattern        = regexp.MustCompile(`^\d{7,8}$`)
	otherDocPattern   = regexp.MustCompile(`^[A-Z0-9]{5,20}$`)
	documentSeparator = strings.NewReplacer(".", "", " ", "", "-", "")
)

var documentTypes = map[string]bool{
	domain.DocumentDNI:       true,
	domain.DocumentPassport:  true,
	domain.DocumentCedula:    true,
	domain.DocumentOtherType: true,
}

// validatePassword aplica la misma política que muestra el formulario de registro.
func validatePassword(password string) error {
	if len(password) < 8 {
		return &fieldError{"password", "La contraseña debe tener al menos 8 caracteres"}
	}
	// bcrypt ignora todo lo que pase de 72 bytes.
	if len(password) > 72 {
		return &fieldError{"password", "La contraseña no puede superar los 72 caracteres"}
	}
	var hasUpper, hasDigit bool
	for _, r := range password {
		hasUpper = hasUpper || unicode.IsUpper(r)
		hasDigit = hasDigit || unicode.IsDigit(r)
	}
	if !hasUpper {
		return &fieldError{"password", "La contraseña debe tener al menos una letra mayúscula"}
	}
	if !hasDigit {
		return &fieldError{"password", "La contraseña debe tener al menos un número"}
	}
	return nil
}

func requiredText(field, label, value string, maxLen int) (string, error) {
	value = strings.Join(strings.Fields(value), " ")
	if value == "" {
		return "", &fieldError{field, label + " es obligatorio"}
	}
	if utf8.RuneCountInString(value) > maxLen {
		return "", &fieldError{field, label + " es demasiado largo"}
	}
	return value, nil
}

// toUser valida y normaliza el formulario de registro.
func (r registerRequest) toUser(now time.Time) (domain.User, error) {
	var u domain.User
	var err error

	u.Email = strings.ToLower(strings.TrimSpace(r.Email))
	if !validEmail(u.Email) {
		return u, &fieldError{"email", "Ingresá un correo electrónico válido"}
	}
	if err := validatePassword(r.Password); err != nil {
		return u, err
	}
	if u.Nombre, err = requiredText("nombre", "El nombre", r.Nombre, 100); err != nil {
		return u, err
	}
	if u.Apellido, err = requiredText("apellido", "El apellido", r.Apellido, 100); err != nil {
		return u, err
	}
	if u.PaisResidencia, err = requiredText("pais_residencia", "El país de residencia", r.PaisResidencia, 60); err != nil {
		return u, err
	}

	u.Telefono = strings.Join(strings.Fields(r.Telefono), " ")
	if !phonePattern.MatchString(u.Telefono) {
		return u, &fieldError{"telefono", "Ingresá el teléfono con código de país y de área (ej. +54 2284 123456)"}
	}

	u.TipoDocumento = strings.ToUpper(strings.TrimSpace(r.TipoDocumento))
	if !documentTypes[u.TipoDocumento] {
		return u, &fieldError{"tipo_documento", "Seleccioná un tipo de documento válido"}
	}
	u.NumeroDNI = strings.ToUpper(documentSeparator.Replace(strings.TrimSpace(r.NumeroDNI)))
	if u.TipoDocumento == domain.DocumentDNI && !dniPattern.MatchString(u.NumeroDNI) {
		return u, &fieldError{"numero_dni", "El DNI debe tener 7 u 8 dígitos"}
	}
	if !otherDocPattern.MatchString(u.NumeroDNI) {
		return u, &fieldError{"numero_dni", "Ingresá un número de documento válido"}
	}

	u.FechaNacimiento, err = time.Parse("2006-01-02", strings.TrimSpace(r.FechaNacimiento))
	if err != nil {
		return u, &fieldError{"fecha_nacimiento", "Ingresá una fecha de nacimiento válida"}
	}
	if !u.FechaNacimiento.Before(now) || u.FechaNacimiento.Year() < now.Year()-120 {
		return u, &fieldError{"fecha_nacimiento", "Ingresá una fecha de nacimiento válida"}
	}

	u.AceptaSuscripcion = r.AceptaSuscripcion
	return u, nil
}

// Register: POST /api/auth/register
func (h *AuthHandler) Register(c *gin.Context) {
	var req registerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Datos de registro inválidos"})
		return
	}
	if !h.verifyCaptcha(c, req.RecaptchaToken) {
		return
	}

	user, err := req.toUser(time.Now())
	if err != nil {
		var fe *fieldError
		if errors.As(err, &fe) {
			c.JSON(http.StatusBadRequest, gin.H{"error": fe.Message, "field": fe.Field})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		log.Printf("Error hashing registration password: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "No se pudo completar el registro"})
		return
	}

	// Los índices UNIQUE (email y documento) resuelven los duplicados sin carreras
	// entre dos registros simultáneos (un SELECT previo no lo garantiza).
	result, err := h.db.ExecContext(
		c.Request.Context(),
		`INSERT INTO usuarios (email, password_hash, nombre, apellido, pais_residencia, telefono,
		                       tipo_documento, numero_dni, fecha_nacimiento, acepta_suscripcion)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		user.Email,
		string(passwordHash),
		user.Nombre,
		user.Apellido,
		user.PaisResidencia,
		user.Telefono,
		user.TipoDocumento,
		user.NumeroDNI,
		user.FechaNacimiento.Format("2006-01-02"),
		user.AceptaSuscripcion,
	)
	if err != nil {
		var mysqlErr *mysqlDriver.MySQLError
		if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
			// MySQL informa qué índice chocó: "Duplicate entry '...' for key 'usuarios.<índice>'".
			if strings.Contains(mysqlErr.Message, documentUniqueIndex) {
				c.JSON(http.StatusConflict, gin.H{"error": "Ya existe una cuenta registrada con ese documento", "field": "numero_dni"})
				return
			}
			c.JSON(http.StatusConflict, gin.H{"error": "Ya existe una cuenta con ese correo electrónico", "field": "email"})
			return
		}
		log.Printf("Error creating user: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "No se pudo completar el registro"})
		return
	}

	userID, err := result.LastInsertId()
	if err != nil {
		log.Printf("Error getting registered user ID: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "No se pudo completar el registro"})
		return
	}
	h.respondWithToken(c, userID, domain.RolUsuario, 0, http.StatusCreated)
}

// Login: POST /api/auth/login (requiere el token de reCAPTCHA v2)
func (h *AuthHandler) Login(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Ingresá tu correo y contraseña"})
		return
	}

	// El captcha se valida antes de tocar la base o bcrypt.
	if !h.verifyCaptcha(c, req.RecaptchaToken) {
		return
	}

	email := strings.ToLower(strings.TrimSpace(req.Email))
	var userID, organizadorID int64
	var passwordHash, rol string
	err := h.db.QueryRowContext(
		c.Request.Context(),
		"SELECT id, password_hash, rol, COALESCE(organizador_id, 0) FROM usuarios WHERE email = ?",
		email,
	).Scan(&userID, &passwordHash, &rol, &organizadorID)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			log.Printf("Error looking up login user: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "No se pudo iniciar sesión"})
			return
		}
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Correo o contraseña incorrectos"})
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(req.Password)); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Correo o contraseña incorrectos"})
		return
	}

	h.respondWithToken(c, userID, rol, organizadorID, http.StatusOK)
}

// verifyCaptcha valida el token de reCAPTCHA v2 y, si falla, ya deja escrita la respuesta.
func (h *AuthHandler) verifyCaptcha(c *gin.Context, token string) bool {
	err := h.captcha.Verify(c.Request.Context(), token, c.ClientIP())
	if err == nil {
		return true
	}
	if errors.Is(err, recaptcha.ErrInvalidToken) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Completá la verificación \"No soy un robot\"", "field": "recaptcha"})
		return false
	}
	log.Printf("Error verificando reCAPTCHA: %v", err)
	c.JSON(http.StatusServiceUnavailable, gin.H{"error": "No se pudo verificar el captcha, intentá de nuevo"})
	return false
}

func (h *AuthHandler) respondWithToken(c *gin.Context, userID int64, rol string, organizadorID int64, status int) {
	token, err := auth.GenerateToken(userID, rol, organizadorID)
	if err != nil {
		log.Printf("Error generating JWT: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "La autenticación no está configurada"})
		return
	}
	c.JSON(status, gin.H{"token": token, "user_id": userID, "rol": rol})
}

func validEmail(email string) bool {
	address, err := mail.ParseAddress(email)
	return err == nil && address.Address == email
}
