package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"stock-api/internal/domain"
	"stock-api/internal/reservation"
	"time"

	"github.com/redis/go-redis/v9"
)

// InternalOrder debe coincidir con lo que enviamos desde el Handler
// (los ítems de la orden se leen de MySQL al reservar).
type InternalOrder struct {
	OrderID int64 `json:"order_id"` // <--- AHORA RECIBIMOS EL ID DE LA ORDEN
	EventID int64 `json:"event_id"`
	UserID  int64 `json:"user_id"`
}

// Start inicia el proceso de escuchar a Redis y escribir en MySQL
func Start(rdb *redis.Client, repo domain.EventRepository, reservations *reservation.Service) {
	fmt.Println("👷 Worker PRO iniciado: Gestionando Órdenes...")

	ctx := context.Background()

	for {
		// 1. Esperar mensaje en Redis
		result, err := rdb.BLPop(ctx, 0*time.Second, "purchase_queue").Result()
		if err != nil {
			// Un Redis en la nube puede cortar la conexión: esperamos antes de reintentar
			// para no llenar el log (ni gastar comandos) en un bucle sin pausa.
			log.Printf("Error leyendo Redis, reintento en 2s: %v", err)
			time.Sleep(2 * time.Second)
			continue
		}

		payload := result[1]
		var order InternalOrder
		if err := json.Unmarshal([]byte(payload), &order); err != nil {
			log.Printf("Error decodificando JSON: %v", err)
			continue
		}

		fmt.Printf("🔄 Procesando Orden #%d (Evento %d)... ", order.OrderID, order.EventID)

		// 2. Intentar reservar el stock de todos los sectores del carrito
		// (descuento + estado 'reservada' en una transacción: todo o nada)
		err = repo.ReserveStock(ctx, order.OrderID, order.EventID, int(reservation.TTL.Seconds()))
		if err != nil {
			if err != domain.ErrNotEnoughStock {
				fmt.Printf("❌ Error MySQL: %v\n", err)
				continue
			}
			// SIN STOCK -> RECHAZAMOS LA ORDEN
			fmt.Println("⛔ RECHAZADA (Sin Stock)")
			if err := repo.UpdateOrderStatus(ctx, order.OrderID, domain.OrderRejected, ""); err != nil {
				log.Printf("Error actualizando estado de la orden #%d: %v", order.OrderID, err)
				continue
			}
			reservations.Publish(ctx, order.OrderID, reservation.StatusUpdate{
				Status:  domain.OrderRejected,
				Message: "Lo sentimos, no quedan entradas suficientes en alguno de los sectores elegidos.",
			})
			continue
		}

		// 3. HAY STOCK -> RESERVADA por TTL, esperando el pago.
		// Si Redis falla acá, el reconciliador del expirador la libera igual vía MySQL.
		if err := reservations.Hold(ctx, order.OrderID); err != nil {
			log.Printf("Error registrando TTL de la orden #%d: %v", order.OrderID, err)
		}
		fmt.Println("🕒 RESERVADA")
		reservations.Publish(ctx, order.OrderID, reservation.StatusUpdate{
			Status:    domain.OrderReserved,
			ExpiresIn: int(reservation.TTL.Seconds()),
		})
	}
}
