package main

import (
	"fmt"
	"log"

	"github.com/gin-gonic/gin"

	// Paquetes locales
	redisClient "stock-api/internal/platform/redis"
	"stock-api/internal/platform/storage/mysql"
	transport "stock-api/internal/transport/http"
	"stock-api/internal/worker" // <--- Importo el worker nuevo
	"time"

	"github.com/gin-contrib/cors"
)

func main() {
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
	handler := transport.NewHandler(repo, rdb)

	// 5. --- LANZAR EL WORKER EN PARALELO (GOROUTINE) ---
	// Le pasamos Redis para leer y el Repo para escribir en MySQL
	go worker.Start(rdb, repo)
	// ---------------------------------------------------

	// 6. Configurar Router
	r := gin.Default()

	// CONFIGURACIÓN DE CORS
	r.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"http://localhost:5173"}, // El puerto de Vite
		AllowMethods:     []string{"GET", "POST"},
		AllowHeaders:     []string{"Origin", "Content-Type"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))

	r.GET("/events/:id", handler.GetEvent)
	r.GET("/events", handler.GetEvents)
	r.POST("/events/:id/comprar", handler.ComprarTickets)
	r.GET("/orders/:id", handler.CheckStatus)

	// 7. Arrancar Servidor
	fmt.Println("🚀 Servidor corriendo en http://localhost:8080 + Worker Activo")
	if err := r.Run(":8080"); err != nil {
		log.Fatalf("❌ Error al iniciar servidor: %v", err)
	}
}
