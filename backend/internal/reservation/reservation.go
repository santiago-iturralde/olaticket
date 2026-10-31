package reservation

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"stock-api/internal/domain"
	"stock-api/internal/platform/auth"
)

// TTL es el tiempo que una orden mantiene el stock reservado esperando el pago.
const TTL = 10 * time.Minute

const (
	// expiryQueue es un sorted set con score = unix timestamp de vencimiento.
	// Lo usamos en vez de keyspace notifications porque esas se pierden si no
	// hay nadie suscripto en el momento exacto en que expira la clave.
	expiryQueue = "reservation_expiry"

	defaultSweepInterval = 5 * time.Second
	reconcileInterval    = time.Minute
	batchSize            = 100
)

// StatusUpdate es el mensaje que viaja por Redis Pub/Sub hacia el SSE del usuario.
type StatusUpdate struct {
	Status    string `json:"status"`
	QRToken   string `json:"qr_token,omitempty"`
	ExpiresIn int    `json:"expires_in,omitempty"`
	Message   string `json:"mensaje,omitempty"`
}

func StatusChannel(orderID int64) string {
	return "order_status_" + strconv.FormatInt(orderID, 10)
}

func reservationKey(orderID int64) string {
	return fmt.Sprintf("reservation:%d", orderID)
}

// Service centraliza las transiciones de una orden reservada:
// reservar (TTL), confirmar (pago aprobado) y liberar (expirada / rechazada).
type Service struct {
	rdb  *redis.Client
	repo domain.EventRepository
}

func NewService(rdb *redis.Client, repo domain.EventRepository) *Service {
	return &Service{rdb: rdb, repo: repo}
}

// Hold registra la reserva en Redis: una clave con TTL (para saber cuánto falta)
// y una entrada en la cola de expiración (para que el expirador la procese).
func (s *Service) Hold(ctx context.Context, orderID int64) error {
	expiresAt := time.Now().Add(TTL)
	pipe := s.rdb.TxPipeline()
	pipe.Set(ctx, reservationKey(orderID), 1, TTL)
	pipe.ZAdd(ctx, expiryQueue, redis.Z{Score: float64(expiresAt.Unix()), Member: orderID})
	_, err := pipe.Exec(ctx)
	return err
}

// TimeLeft devuelve el tiempo restante de la reserva (0 si ya venció).
func (s *Service) TimeLeft(ctx context.Context, orderID int64) (time.Duration, error) {
	ttl, err := s.rdb.TTL(ctx, reservationKey(orderID)).Result()
	if err != nil {
		return 0, err
	}
	if ttl < 0 {
		return 0, nil
	}
	return ttl, nil
}

// Confirm pasa la orden a 'confirmada', genera el QR firmado y avisa al SSE.
// Retorna el qr_token emitido, o "" si la orden ya no estaba reservada.
func (s *Service) Confirm(ctx context.Context, order *domain.Order, paymentID string) (string, error) {
	qrToken, err := auth.GenerateTicketToken(order.ID, order.UserID, order.EventID)
	if err != nil {
		return "", fmt.Errorf("error generating QR: %w", err)
	}
	confirmed, err := s.repo.ConfirmOrder(ctx, order.ID, paymentID, qrToken)
	if err != nil || !confirmed {
		return "", err
	}
	s.clear(ctx, order.ID)
	s.Publish(ctx, order.ID, StatusUpdate{Status: domain.OrderConfirmed, QRToken: qrToken})
	return qrToken, nil
}

// Release pasa la orden a 'expirada' o 'rechazada' y devuelve (+cantidad) el stock.
// Es idempotente: si la orden ya no estaba reservada no hace nada.
func (s *Service) Release(ctx context.Context, orderID int64, status, message string) (bool, error) {
	released, err := s.repo.ReleaseReservation(ctx, orderID, status)
	if err != nil {
		return false, err
	}
	s.clear(ctx, orderID)
	if released {
		s.Publish(ctx, orderID, StatusUpdate{Status: status, Message: message})
	}
	return released, nil
}

// NotifyCancelled limpia Redis y avisa por SSE una orden que MySQL ya pasó a 'rechazada'
// por fuera del flujo de pago (por ejemplo, porque el organizador eliminó el evento).
func (s *Service) NotifyCancelled(ctx context.Context, orderID int64, message string) {
	s.clear(ctx, orderID)
	s.Publish(ctx, orderID, StatusUpdate{Status: domain.OrderRejected, Message: message})
}

func (s *Service) Publish(ctx context.Context, orderID int64, update StatusUpdate) {
	message, err := json.Marshal(update)
	if err != nil {
		log.Printf("Error serializando estado de la orden #%d: %v", orderID, err)
		return
	}
	if err := s.rdb.Publish(ctx, StatusChannel(orderID), message).Err(); err != nil {
		log.Printf("Error publicando estado de la orden #%d: %v", orderID, err)
	}
}

func (s *Service) clear(ctx context.Context, orderID int64) {
	pipe := s.rdb.TxPipeline()
	pipe.Del(ctx, reservationKey(orderID))
	pipe.ZRem(ctx, expiryQueue, orderID)
	if _, err := pipe.Exec(ctx); err != nil {
		log.Printf("Error limpiando reserva #%d en Redis: %v", orderID, err)
	}
}

// sweepInterval es cada cuánto se buscan reservas vencidas en Redis. Cada barrido es un
// comando: con un Redis que cobra o limita por comando (Upstash) conviene subirlo con
// RESERVATION_SWEEP_SECONDS (ej. 30). Una reserva de 10 minutos vence a lo sumo ese tiempo
// tarde, y el reconciliador de MySQL sigue cubriendo lo que Redis pierda.
var sweepInterval = sweepIntervalFromEnv()

func sweepIntervalFromEnv() time.Duration {
	seconds, err := strconv.Atoi(os.Getenv("RESERVATION_SWEEP_SECONDS"))
	if err != nil || seconds < 1 || seconds > 300 {
		return defaultSweepInterval
	}
	return time.Duration(seconds) * time.Second
}

// RunExpirer libera las reservas vencidas. Corre en su propia goroutine.
// Con varias instancias de la API es seguro: ZREM reclama cada orden una sola vez
// y ReleaseReservation es condicional sobre el estado 'reservada'.
func (s *Service) RunExpirer(ctx context.Context) {
	fmt.Println("⏳ Expirador de reservas iniciado (TTL", TTL, ", barrido cada", sweepInterval, ")")
	sweep := time.NewTicker(sweepInterval)
	reconcile := time.NewTicker(reconcileInterval)
	defer sweep.Stop()
	defer reconcile.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-sweep.C:
			s.expireDue(ctx)
		case <-reconcile.C:
			s.reconcile(ctx)
		}
	}
}

func (s *Service) expireDue(ctx context.Context) {
	due, err := s.rdb.ZRangeByScore(ctx, expiryQueue, &redis.ZRangeBy{
		Min:   "-inf",
		Max:   strconv.FormatInt(time.Now().Unix(), 10),
		Count: batchSize,
	}).Result()
	if err != nil {
		log.Printf("Error leyendo reservas vencidas: %v", err)
		return
	}

	for _, member := range due {
		orderID, err := strconv.ParseInt(member, 10, 64)
		if err != nil {
			s.rdb.ZRem(ctx, expiryQueue, member)
			continue
		}
		// Reclamamos la orden: solo una instancia obtiene 1.
		claimed, err := s.rdb.ZRem(ctx, expiryQueue, member).Result()
		if err != nil || claimed == 0 {
			continue
		}
		s.expire(ctx, orderID)
	}
}

// reconcile es la red de seguridad: busca en MySQL reservas vencidas que Redis
// no tenga registradas (por ejemplo tras un FLUSHALL o un reinicio de Redis).
func (s *Service) reconcile(ctx context.Context) {
	ids, err := s.repo.GetExpiredReservations(ctx, batchSize)
	if err != nil {
		log.Printf("Error reconciliando reservas vencidas: %v", err)
		return
	}
	for _, orderID := range ids {
		s.expire(ctx, orderID)
	}
}

func (s *Service) expire(ctx context.Context, orderID int64) {
	released, err := s.Release(ctx, orderID, domain.OrderExpired, "Tu reserva expiró y las entradas fueron liberadas.")
	if err != nil {
		log.Printf("Error expirando orden #%d, se reintenta: %v", orderID, err)
		retryAt := time.Now().Add(sweepInterval).Unix()
		s.rdb.ZAdd(ctx, expiryQueue, redis.Z{Score: float64(retryAt), Member: orderID})
		return
	}
	if released {
		fmt.Printf("⌛ Orden #%d EXPIRADA: stock devuelto\n", orderID)
	}
}
