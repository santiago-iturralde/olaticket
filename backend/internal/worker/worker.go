package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"stock-api/internal/domain"
	"time"

	"github.com/redis/go-redis/v9"
)

// InternalOrder debe coincidir con lo que enviamos desde el Handler
type InternalOrder struct {
	OrderID  int64 `json:"order_id"` // <--- AHORA RECIBIMOS EL ID DE LA ORDEN
	EventID  int64 `json:"event_id"`
	Quantity int   `json:"quantity"`
}

// Start inicia el proceso de escuchar a Redis y escribir en MySQL
func Start(rdb *redis.Client, repo domain.EventRepository) {
	fmt.Println("👷 Worker PRO iniciado: Gestionando Órdenes...")

	ctx := context.Background()

	for {
		// 1. Esperar mensaje en Redis
		result, err := rdb.BLPop(ctx, 0*time.Second, "purchase_queue").Result()
		if err != nil {
			log.Printf("Error leyendo Redis: %v", err)
			continue
		}

		payload := result[1]
		var order InternalOrder
		if err := json.Unmarshal([]byte(payload), &order); err != nil {
			log.Printf("Error decodificando JSON: %v", err)
			continue
		}

		fmt.Printf("🔄 Procesando Orden #%d (Evento %d)... ", order.OrderID, order.EventID)

		// 2. Intentar Bajar Stock
		err = repo.DecreaseStock(ctx, order.EventID, order.Quantity)

		if err != nil {
			// SI FALLA (No hay stock o error) -> RECHAZAMOS LA ORDEN
			if err == domain.ErrNotEnoughStock {
				_ = repo.UpdateOrderStatus(ctx, order.OrderID, "rechazada")
				fmt.Println("⛔ RECHAZADA (Sin Stock)")
			} else {
				fmt.Printf("❌ Error MySQL: %v\n", err)
				// Opcional: Podrías marcarla como "error" o reintentar
			}
		} else {
			// SI FUNCIONA -> CONFIRMAMOS LA ORDEN
			_ = repo.UpdateOrderStatus(ctx, order.OrderID, "confirmada")
			fmt.Println("✅ CONFIRMADA")
		}
	}
}
