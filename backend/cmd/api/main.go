package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"

	// Paquetes locales
	"stock-api/internal/domain"
	"stock-api/internal/platform/auth"
	"stock-api/internal/platform/cloudinary"
	"stock-api/internal/platform/email"
	"stock-api/internal/platform/metrics"
	"stock-api/internal/platform/payment"
	"stock-api/internal/platform/ratelimit"
	"stock-api/internal/platform/recaptcha"
	redisClient "stock-api/internal/platform/redis"
	"stock-api/internal/platform/storage/mysql"
	"stock-api/internal/reservation"
	transport "stock-api/internal/transport/http"
	"stock-api/internal/worker" // <--- Importo el worker nuevo
	"time"

	"github.com/gin-contrib/cors"
)

func main() {
	if err := auth.ValidateConfiguration(); err != nil {
		log.Fatalf("❌ Error JWT: %v", err)
	}

	// 1. Conexión a MySQL
	db, err := mysql.NewConnection()
	if err != nil {
		log.Fatalf("❌ Error db: %v", err)
	}
	defer db.Close()
	fmt.Println("✅ Base de Datos (MySQL) conectada.")

	// 2. Conexión a Redis
	rdb, err := redisClient.NewConnection()
	if err != nil {
		log.Fatalf("❌ Error Redis: %v", err)
	}
	defer rdb.Close()
	fmt.Println("✅ Redis conectado exitosamente.")

	// 3. Inicializar Repositorio
	repo := mysql.NewRepository(db)

	// 4. Iniciar el Handler (AHORA RECIBE REDIS TAMBIÉN)
	reservations := reservation.NewService(rdb, repo)
	paymentConfig := payment.ConfigFromEnv()
	gateway := payment.NewGateway(paymentConfig)

	handler := transport.NewHandler(repo, rdb, reservations)
	emailConfig := email.ConfigFromEnv()
	if !emailConfig.Enabled() {
		log.Println("⚠️  SMTP_USER / SMTP_PASS no configurados: las entradas y la recuperación de contraseña no se enviarán por email")
	}
	mailer := email.NewSender(emailConfig)
	captcha := recaptcha.FromEnv()
	if !captcha.Enabled() {
		log.Println("⚠️  RECAPTCHA_SECRET no configurado: login y registro NO validan reCAPTCHA (solo para desarrollo local)")
	}
	authHandler := transport.NewAuthHandler(db, rdb, captcha, mailer, paymentConfig.FrontendURL)
	paymentHandler := transport.NewPaymentHandler(repo, reservations, gateway, mailer, paymentConfig.FrontendURL)
	cloudinaryConfig, err := cloudinary.ConfigFromEnv()
	if err != nil {
		log.Fatalf("❌ Error Cloudinary: %v", err)
	}
	images := cloudinary.NewUploader(cloudinaryConfig)
	if !images.Enabled() {
		log.Println("⚠️  CLOUDINARY_URL no configurado: no se pueden subir portadas ni mapas de eventos")
	}
	adminHandler := transport.NewAdminHandler(repo, reservations, images)
	usersHandler := transport.NewUsersHandler(repo)
	productorasHandler := transport.NewProductorasHandler(repo)

	// 5. --- LANZAR EL WORKER Y EL EXPIRADOR EN PARALELO (GOROUTINES) ---
	// Le pasamos Redis para leer y el Repo para escribir en MySQL
	go worker.Start(rdb, repo, reservations)
	go reservations.RunExpirer(context.Background())
	// ---------------------------------------------------

	// 6. Configurar Router
	r := gin.Default()
	if err := r.SetTrustedProxies(nil); err != nil {
		log.Fatalf("❌ Error configurando proxies confiables: %v", err)
	}

	// Métricas de Prometheus: el middleware va primero para medir también lo que cortan
	// CORS, auth o el rate limit. /metrics se sirve en un puerto aparte (METRICS_PORT), no
	// en el público: expone tráfico y rutas internas y solo debe verlo Prometheus.
	httpMetrics := metrics.New()
	r.Use(httpMetrics.Middleware())
	go serveMetrics(httpMetrics)

	// CONFIGURACIÓN DE CORS
	r.Use(cors.New(cors.Config{
		AllowOrigins:     allowedOrigins(paymentConfig.FrontendURL),
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))

	r.GET("/events/:id", handler.GetEvent)
	r.GET("/events", handler.GetEvents)
	authRoutes := r.Group("/api/auth")
	authRoutes.POST("/register", ratelimit.RateLimiterMiddleware(rdb, 5, time.Minute), authHandler.Register)
	authRoutes.POST("/login", ratelimit.RateLimiterMiddleware(rdb, 10, time.Minute), authHandler.Login)
	authRoutes.POST("/forgot-password", ratelimit.RateLimiterMiddleware(rdb, 3, time.Minute), authHandler.ForgotPassword)
	authRoutes.POST("/reset-password", ratelimit.RateLimiterMiddleware(rdb, 5, time.Minute), authHandler.ResetPassword)
	r.POST("/events/:id/comprar",
		ratelimit.RateLimiterMiddleware(rdb, 5, time.Minute),
		auth.AuthMiddleware(),
		handler.ComprarTickets,
	)
	r.GET("/orders/:id", auth.AuthMiddleware(), handler.CheckStatus)
	r.GET("/orders/:id/stream", auth.AuthMiddleware(), handler.StreamOrderStatus)

	// Pagos (Mercado Pago)
	r.POST("/orders/:id/checkout",
		ratelimit.RateLimiterMiddleware(rdb, 10, time.Minute),
		auth.AuthMiddleware(),
		paymentHandler.Checkout,
	)
	r.POST("/payments/webhook", paymentHandler.Webhook)
	if gateway.IsMock() {
		r.GET("/payments/mock/checkout", paymentHandler.MockCheckoutPage)
		r.POST("/payments/mock/checkout", paymentHandler.MockCheckoutSubmit)
	}

	// /api/admin: equipo (admins, organizadores y porteros), siempre limitado a su productora.
	// CurrentRole toma el rol de la base: un cambio de rol rige en el próximo request, no
	// cuando vence el JWT.
	staffRoutes := r.Group("/api/admin", auth.AuthMiddleware(), transport.CurrentRole(repo))

	// Control de acceso en la puerta: también los porteros (solo eventos de su productora).
	scannerRoutes := staffRoutes.Group("", auth.RequireRole(domain.RolPortero, domain.RolOrganizador, domain.RolAdmin))
	scannerRoutes.GET("/escaner/eventos", adminHandler.ScannerEvents)
	scannerRoutes.POST("/eventos/:id/scan", adminHandler.ScanTicket) // :id = evento_id

	// Backoffice: organizadores (sobre su productora) y admins. Los porteros no entran.
	adminRoutes := staffRoutes.Group("", auth.RequireRole(domain.RolOrganizador, domain.RolAdmin))
	adminRoutes.GET("/eventos", adminHandler.ListEvents)
	adminRoutes.GET("/eventos/:id", adminHandler.GetEvent)
	adminRoutes.POST("/eventos", adminHandler.CreateEvent)
	adminRoutes.PUT("/eventos/:id", adminHandler.UpdateEvent)
	adminRoutes.DELETE("/eventos/:id", adminHandler.DeleteEvent)
	adminRoutes.GET("/organizadores", auth.RequireRole(domain.RolAdmin), adminHandler.ListOrganizadores)
	// Usuarios: el admin gestiona a todo el equipo; el organizador, a los de su productora.
	adminRoutes.GET("/usuarios", usersHandler.ListUsers)
	adminRoutes.POST("/usuarios", usersHandler.CreateUser)
	adminRoutes.PUT("/usuarios/:id", usersHandler.UpdateUser)
	adminRoutes.DELETE("/usuarios/:id", usersHandler.DeleteUser)
	adminRoutes.PUT("/usuarios/:id/rol", usersHandler.UpdateRole)

	// Productoras (tenants): solo admin
	productoraRoutes := adminRoutes.Group("/productoras", auth.RequireRole(domain.RolAdmin))
	productoraRoutes.GET("", productorasHandler.List)
	productoraRoutes.POST("", productorasHandler.Create)
	productoraRoutes.PUT("/:id", productorasHandler.Update)
	productoraRoutes.DELETE("/:id", productorasHandler.Delete)

	// 7. Arrancar Servidor
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	fmt.Printf("🚀 Servidor corriendo en el puerto %s + Worker Activo\n", port)
	if err := r.Run(":" + port); err != nil {
		log.Fatalf("❌ Error al iniciar servidor: %v", err)
	}
}

// serveMetrics expone GET /metrics en METRICS_PORT (por defecto 9091).
func serveMetrics(m *metrics.Metrics) {
	port := os.Getenv("METRICS_PORT")
	if port == "" {
		port = "9091"
	}
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", m.Handler())
	server := &http.Server{Addr: ":" + port, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	fmt.Printf("📈 Métricas de Prometheus en :%s/metrics\n", port)
	if err := server.ListenAndServe(); err != nil {
		log.Printf("⚠️  El servidor de métricas se detuvo: %v", err)
	}
}

func allowedOrigins(frontendURL string) []string {
	origins := []string{"http://localhost:5173", "http://localhost"}
	for _, origin := range origins {
		if origin == frontendURL {
			return origins
		}
	}
	return append(origins, frontendURL)
}
