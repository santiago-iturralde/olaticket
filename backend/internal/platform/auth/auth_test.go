package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestGenerateAndValidateToken(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret-with-at-least-32-characters")

	token, err := GenerateToken(42, "usuario", 0)
	if err != nil {
		t.Fatalf("GenerateToken() error = %v", err)
	}

	claims, err := ValidateToken(token)
	if err != nil {
		t.Fatalf("ValidateToken() error = %v", err)
	}
	if claims.UserID != 42 {
		t.Fatalf("ValidateToken() user ID = %d, want 42", claims.UserID)
	}

	if _, err := ValidateToken(token + "invalid"); err == nil {
		t.Fatal("ValidateToken() accepted a tampered token")
	}
}

func TestGenerateAndValidateTicketToken(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret-with-at-least-32-characters")

	token, err := GenerateTicketToken(7, 42, 9)
	if err != nil {
		t.Fatalf("GenerateTicketToken() error = %v", err)
	}

	claims, err := ValidateTicketToken(token)
	if err != nil {
		t.Fatalf("ValidateTicketToken() error = %v", err)
	}
	if claims.OrderID != 7 || claims.UserID != 42 || claims.EventID != 9 || claims.TokenUse != "ticket" {
		t.Fatalf("ticket claims = %+v, want order=7 user=42 event=9 use=ticket", claims)
	}

	if _, err := ValidateTicketToken(token + "invalid"); err == nil {
		t.Fatal("ValidateTicketToken() accepted a tampered token")
	}
	if _, err := ValidateToken(token); err == nil {
		t.Fatal("ValidateToken() accepted a ticket token as an authentication token")
	}
}

func TestAuthMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("JWT_SECRET", "test-secret-with-at-least-32-characters")

	token, err := GenerateToken(42, "usuario", 0)
	if err != nil {
		t.Fatalf("GenerateToken() error = %v", err)
	}

	router := gin.New()
	router.GET("/protected", AuthMiddleware(), func(c *gin.Context) {
		c.String(http.StatusOK, "%d", c.GetInt64(UserIDContextKey))
	})

	request := httptest.NewRequest(http.MethodGet, "/protected", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("request without token status = %d, want %d", response.Code, http.StatusUnauthorized)
	}

	request = httptest.NewRequest(http.MethodGet, "/protected", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "42" {
		t.Fatalf("authenticated request = %d, %q; want 200, 42", response.Code, response.Body.String())
	}
}

func TestRequireRole(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("JWT_SECRET", "test-secret-with-at-least-32-characters")

	router := gin.New()
	router.GET("/admin", AuthMiddleware(), RequireRole("organizador", "admin"), func(c *gin.Context) {
		c.String(http.StatusOK, "%s %d", c.GetString(RolContextKey), c.GetInt64(OrganizadorIDContextKey))
	})

	cases := []struct {
		rol           string
		organizadorID int64
		wantStatus    int
		wantBody      string
	}{
		{"usuario", 0, http.StatusForbidden, ""},
		{"", 0, http.StatusForbidden, ""}, // token previo a la fase 4
		{"organizador", 7, http.StatusOK, "organizador 7"},
		{"admin", 0, http.StatusOK, "admin 0"},
	}
	for _, tc := range cases {
		token, err := GenerateToken(42, tc.rol, tc.organizadorID)
		if err != nil {
			t.Fatalf("GenerateToken() error = %v", err)
		}
		request := httptest.NewRequest(http.MethodGet, "/admin", nil)
		request.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != tc.wantStatus || (tc.wantBody != "" && response.Body.String() != tc.wantBody) {
			t.Errorf("rol %q: got %d %q, want %d %q", tc.rol, response.Code, response.Body.String(), tc.wantStatus, tc.wantBody)
		}
	}
}

func TestGenerateTokenRequiresSecret(t *testing.T) {
	t.Setenv("JWT_SECRET", "")

	if _, err := GenerateToken(42, "usuario", 0); err != ErrSecretNotConfigured {
		t.Fatalf("GenerateToken() error = %v, want %v", err, ErrSecretNotConfigured)
	}
}
