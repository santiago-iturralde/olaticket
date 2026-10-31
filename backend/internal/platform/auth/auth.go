package auth

import (
	"errors"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

const (
	UserIDContextKey        = "userID"
	RolContextKey           = "rol"
	OrganizadorIDContextKey = "organizadorID"
)

var ErrSecretNotConfigured = errors.New("JWT_SECRET is not configured")

// Claims del token de acceso. Rol y OrganizadorID se copian de la base al hacer login:
// un cambio de rol recién se refleja cuando el usuario obtiene un token nuevo.
type Claims struct {
	UserID        int64  `json:"user_id"`
	Rol           string `json:"rol"`
	OrganizadorID int64  `json:"organizador_id,omitempty"`
	TokenUse      string `json:"token_use"`
	jwt.RegisteredClaims
}

type TicketClaims struct {
	OrderID  int64  `json:"order_id"`
	UserID   int64  `json:"user_id"`
	EventID  int64  `json:"event_id"`
	TokenUse string `json:"token_use"`
	jwt.RegisteredClaims
}

func ValidateConfiguration() error {
	_, err := jwtSecret()
	return err
}

func jwtSecret() ([]byte, error) {
	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		return nil, ErrSecretNotConfigured
	}
	return []byte(secret), nil
}

func GenerateToken(userID int64, rol string, organizadorID int64) (string, error) {
	secret, err := jwtSecret()
	if err != nil {
		return "", err
	}

	claims := Claims{
		UserID:        userID,
		Rol:           rol,
		OrganizadorID: organizadorID,
		TokenUse:      "access",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    "olaticket",
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(secret)
}

func GenerateTicketToken(orderID, userID, eventID int64) (string, error) {
	secret, err := jwtSecret()
	if err != nil {
		return "", err
	}
	now := time.Now()
	claims := TicketClaims{
		OrderID:  orderID,
		UserID:   userID,
		EventID:  eventID,
		TokenUse: "ticket",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(now),
			Issuer:    "olaticket",
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(secret)
}

func ValidateTicketToken(tokenString string) (*TicketClaims, error) {
	secret, err := jwtSecret()
	if err != nil {
		return nil, err
	}

	claims := &TicketClaims{}
	token, err := jwt.ParseWithClaims(
		tokenString,
		claims,
		func(token *jwt.Token) (any, error) {
			if token.Method != jwt.SigningMethodHS256 {
				return nil, errors.New("unexpected JWT signing method")
			}
			return secret, nil
		},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer("olaticket"),
	)
	if err != nil {
		return nil, err
	}
	if !token.Valid || claims.TokenUse != "ticket" || claims.OrderID <= 0 || claims.UserID <= 0 || claims.EventID <= 0 {
		return nil, errors.New("invalid ticket claims")
	}
	return claims, nil
}

func ValidateToken(tokenString string) (*Claims, error) {
	secret, err := jwtSecret()
	if err != nil {
		return nil, err
	}

	claims := &Claims{}
	token, err := jwt.ParseWithClaims(
		tokenString,
		claims,
		func(token *jwt.Token) (any, error) {
			if token.Method != jwt.SigningMethodHS256 {
				return nil, errors.New("unexpected JWT signing method")
			}
			return secret, nil
		},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer("olaticket"),
	)
	if err != nil {
		return nil, err
	}
	if !token.Valid || claims.UserID <= 0 || claims.TokenUse != "access" {
		return nil, errors.New("invalid JWT claims")
	}
	return claims, nil
}

func AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		parts := strings.Fields(c.GetHeader("Authorization"))
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing or invalid authorization token"})
			return
		}

		claims, err := ValidateToken(parts[1])
		if err != nil {
			if errors.Is(err, ErrSecretNotConfigured) {
				c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "authentication is not configured"})
				return
			}
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired token"})
			return
		}

		c.Set(UserIDContextKey, claims.UserID)
		c.Set(RolContextKey, claims.Rol)
		c.Set(OrganizadorIDContextKey, claims.OrganizadorID)
		c.Next()
	}
}

// RequireRole deja pasar solo a los usuarios cuyo rol (tomado del JWT) está en roles.
// Va siempre después de AuthMiddleware. Los tokens emitidos antes de la fase 4 no
// traen rol y quedan rechazados hasta que el usuario vuelva a iniciar sesión.
func RequireRole(roles ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if slices.Contains(roles, c.GetString(RolContextKey)) {
			c.Next()
			return
		}
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "insufficient permissions"})
	}
}
