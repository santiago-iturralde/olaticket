package http

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"

	"stock-api/internal/platform/email"
)

// PasswordResetTTL es la vida útil del enlace de recuperación.
const PasswordResetTTL = 15 * time.Minute

// En Redis guardamos el SHA-256 del token, nunca el token en claro: quien lea Redis
// no puede armar un enlace válido. La segunda clave permite invalidar el enlace
// anterior cuando el usuario pide uno nuevo.
func passwordResetKey(tokenHash string) string { return "password_reset:" + tokenHash }
func passwordResetUserKey(userID int64) string {
	return "password_reset_user:" + strconv.FormatInt(userID, 10)
}

func hashResetToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// Misma respuesta exista o no la cuenta, para no revelar qué emails están registrados.
const forgotPasswordResponse = "Si el correo está registrado, te enviamos un enlace para restablecer tu contraseña. Revisá tu bandeja de entrada (y spam)."

type forgotPasswordRequest struct {
	Email string `json:"email" binding:"required"`
}

type resetPasswordRequest struct {
	Token    string `json:"token" binding:"required"`
	Password string `json:"password" binding:"required"`
}

// ForgotPassword: POST /api/auth/forgot-password
func (h *AuthHandler) ForgotPassword(c *gin.Context) {
	var req forgotPasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Ingresá tu correo electrónico"})
		return
	}
	address := strings.ToLower(strings.TrimSpace(req.Email))
	if !validEmail(address) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Ingresá un correo electrónico válido", "field": "email"})
		return
	}
	ctx := c.Request.Context()

	var userID int64
	var nombre string
	err := h.db.QueryRowContext(ctx, "SELECT id, nombre FROM usuarios WHERE email = ?", address).Scan(&userID, &nombre)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusOK, gin.H{"mensaje": forgotPasswordResponse})
			return
		}
		log.Printf("Error buscando usuario para recuperar contraseña: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "No se pudo procesar la solicitud"})
		return
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		log.Printf("Error generando token de recuperación: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "No se pudo procesar la solicitud"})
		return
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	tokenHash := hashResetToken(token)

	previousHash, err := h.rdb.Get(ctx, passwordResetUserKey(userID)).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		log.Printf("Error leyendo token de recuperación previo del usuario #%d: %v", userID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "No se pudo procesar la solicitud"})
		return
	}
	pipe := h.rdb.TxPipeline()
	if previousHash != "" {
		pipe.Del(ctx, passwordResetKey(previousHash))
	}
	pipe.Set(ctx, passwordResetKey(tokenHash), userID, PasswordResetTTL)
	pipe.Set(ctx, passwordResetUserKey(userID), tokenHash, PasswordResetTTL)
	if _, err := pipe.Exec(ctx); err != nil {
		log.Printf("Error guardando token de recuperación del usuario #%d: %v", userID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "No se pudo procesar la solicitud"})
		return
	}

	// El envío va en segundo plano: el SMTP no demora la respuesta y el tiempo de
	// respuesta no delata si la cuenta existe.
	link := h.frontendURL + "/?reset_token=" + url.QueryEscape(token)
	go h.sendPasswordResetEmail(userID, address, nombre, link)

	c.JSON(http.StatusOK, gin.H{"mensaje": forgotPasswordResponse})
}

// ResetPassword: POST /api/auth/reset-password
func (h *AuthHandler) ResetPassword(c *gin.Context) {
	var req resetPasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Datos inválidos"})
		return
	}
	// Validamos la clave antes de consumir el token, así un error de tipeo no quema el enlace.
	if err := validatePassword(req.Password); err != nil {
		var fe *fieldError
		if errors.As(err, &fe) {
			c.JSON(http.StatusBadRequest, gin.H{"error": fe.Message, "field": fe.Field})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx := c.Request.Context()

	// GETDEL lo consume de forma atómica: el enlace sirve una sola vez aunque llegue dos veces.
	userID, err := h.rdb.GetDel(ctx, passwordResetKey(hashResetToken(strings.TrimSpace(req.Token)))).Int64()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "El enlace es inválido o expiró. Pedí uno nuevo.", "field": "token"})
			return
		}
		log.Printf("Error consumiendo token de recuperación: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "No se pudo restablecer la contraseña"})
		return
	}
	if err := h.rdb.Del(ctx, passwordResetUserKey(userID)).Err(); err != nil {
		log.Printf("Error limpiando token de recuperación del usuario #%d: %v", userID, err)
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		log.Printf("Error hashing reset password: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "No se pudo restablecer la contraseña"})
		return
	}
	result, err := h.db.ExecContext(ctx, "UPDATE usuarios SET password_hash = ? WHERE id = ?", string(passwordHash), userID)
	if err != nil {
		log.Printf("Error actualizando contraseña del usuario #%d: %v", userID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "No se pudo restablecer la contraseña"})
		return
	}
	if rows, err := result.RowsAffected(); err == nil && rows == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "El enlace es inválido o expiró. Pedí uno nuevo.", "field": "token"})
		return
	}

	log.Printf("🔑 Contraseña restablecida para el usuario #%d", userID)
	c.JSON(http.StatusOK, gin.H{"mensaje": "Tu contraseña fue actualizada. Ya podés iniciar sesión."})
}

var passwordResetEmailTemplate = template.Must(template.New("password-reset").Parse(`<!doctype html>
<html lang="es"><body style="margin:0;padding:24px;background:#f5f5f5;font-family:sans-serif;color:#222">
<div style="max-width:480px;margin:0 auto;background:#fff;border-radius:12px;padding:32px">
<h2 style="margin-top:0;color:#009ee3">Restablecé tu contraseña</h2>
<p>Hola{{if .Nombre}} {{.Nombre}}{{end}}, recibimos un pedido para restablecer la contraseña de tu cuenta de Olaticket.</p>
<p style="text-align:center;margin:28px 0">
  <a href="{{.Link}}" style="background:#009ee3;color:#fff;padding:12px 24px;border-radius:6px;text-decoration:none;font-weight:bold">Crear nueva contraseña</a>
</p>
<p style="color:#666;font-size:13px">El enlace vence en {{.Minutes}} minutos y sirve una sola vez. Si no fuiste vos, ignorá este correo: tu contraseña no cambia.</p>
</div></body></html>`))

func (h *AuthHandler) sendPasswordResetEmail(userID int64, to, nombre, link string) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("❌ Panic enviando el email de recuperación del usuario #%d: %v", userID, r)
		}
	}()
	if !h.mailer.Enabled() {
		// Sin SMTP (desarrollo local) dejamos el enlace en el log para poder probar el flujo.
		log.Printf("⚠️  SMTP no configurado. Enlace de recuperación del usuario #%d: %s", userID, link)
		return
	}

	var body bytes.Buffer
	if err := passwordResetEmailTemplate.Execute(&body, gin.H{
		"Nombre":  nombre,
		"Link":    link,
		"Minutes": int(PasswordResetTTL.Minutes()),
	}); err != nil {
		log.Printf("❌ Error armando el email de recuperación del usuario #%d: %v", userID, err)
		return
	}
	if err := h.mailer.Send(email.Message{
		To:      to,
		Subject: "Restablecé tu contraseña de Olaticket",
		HTML:    body.String(),
	}); err != nil {
		log.Printf("❌ Error enviando el email de recuperación del usuario #%d: %v", userID, err)
		return
	}
	log.Printf("📧 Email de recuperación enviado al usuario #%d", userID)
}
