package http

import (
	"encoding/json"
	"net/http"
	"stock-api/internal/domain"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// Handler contiene las dependencias.
type Handler struct {
	repo domain.EventRepository
	rdb  *redis.Client
}

// NewHandler crea el manejador inyectando Repo y Redis.
func NewHandler(repo domain.EventRepository, rdb *redis.Client) *Handler {
	return &Handler{
		repo: repo,
		rdb:  rdb,
	}
}

// GetEvent maneja la ruta GET /events/:id
func (h *Handler) GetEvent(c *gin.Context) {
	idParam := c.Param("id")
	id, err := strconv.ParseInt(idParam, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	event, err := h.repo.GetByID(c.Request.Context(), id)
	if err != nil {
		if err == domain.ErrEventNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "event not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	c.JSON(http.StatusOK, event)
}

// purchaseRequest define lo que llega en el JSON del body
type purchaseRequest struct {
	UserID int64 `json:"user_id" binding:"required"`
}

// InternalOrder define lo que enviamos a Redis para que el Worker lo lea.
type InternalOrder struct {
	OrderID  int64 `json:"order_id"` // <--- Importante: El ID de la orden para seguimiento
	EventID  int64 `json:"event_id"`
	Quantity int   `json:"quantity"`
}

// ComprarTickets: Crea orden en BD (Pendiente) -> Envía a Cola -> Responde al usuario
// ComprarTickets: Crea orden en BD (Pendiente) -> Envía a Cola -> Responde al usuario
func (h *Handler) ComprarTickets(c *gin.Context) {
	// 1. Validar ID del evento
	idParam := c.Param("id")
	eventID, err := strconv.ParseInt(idParam, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	// 2. Validar Body (Ahora esperamos user_id)
	var req purchaseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		// Aquí se verá el error detallado si algo falla
		c.JSON(http.StatusBadRequest, gin.H{"error": "JSON inválido: " + err.Error()})
		return
	}

	// Como el frontend no tiene selector de cantidad, asumimos 1 por defecto
	quantity := 1

	// 3. --- REGISTRAR ORDEN EN MYSQL ---
	// Usamos el req.UserID que vino de React
	orderID, err := h.repo.CreateOrder(c.Request.Context(), eventID, int(req.UserID))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "error creating order in database"})
		return
	}

	// 4. --- EMPAQUETAR PARA REDIS ---
	orderPayload := InternalOrder{
		OrderID:  orderID,
		EventID:  eventID,
		Quantity: quantity, // Usamos el 1 que definimos arriba
	}

	jsonBody, err := json.Marshal(orderPayload)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "error packing order"})
		return
	}

	// 5. Empujar a la cola "purchase_queue"
	// RECORDATORIO: RPush para FIFO (El primero que llega, gana)
	err = h.rdb.RPush(c.Request.Context(), "purchase_queue", jsonBody).Err()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "error enqueuing order"})
		return
	}

	// 6. Responder al usuario
	c.JSON(http.StatusOK, gin.H{
		"message":  "Solicitud recibida. Tu compra está siendo procesada.",
		"order_id": orderID,
		"status":   "procesando",
	})
}

// CheckStatus: GET /orders/:id
// CheckStatus: GET /orders/:id
func (h *Handler) CheckStatus(c *gin.Context) {
	// 1. Leer ID de la URL
	idParam := c.Param("id")
	orderID, err := strconv.ParseInt(idParam, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ID inválido"})
		return
	}

	// 2. Consultar el estado en la Base de Datos
	// NOTA: Necesitamos saber también el EventID para calcular la fila,
	// así que haremos una pequeña query extra o asumimos que el repo GetOrder nos da info.
	// Para simplificar, asumiremos que GetOrder ahora devuelve estado y eventID,
	// O simplemente hacemos la query de posición asumiendo que el repo la resuelve.

	// ⚠️ TRUCO RÁPIDO: Vamos a leer el estado simple primero.
	estado, err := h.repo.GetOrder(c.Request.Context(), orderID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Orden no encontrada"})
		return
	}

	mensaje := ""
	position := 0 // 0 significa que no aplica (ya comprado o rechazado)

	switch estado {
	case "pendiente":
		// 🚨 MAGIA: Si está pendiente, calculamos la posición
		// (Nota: Para hacerlo perfecto necesitaríamos el eventID.
		//  Por ahora hardcodearemos eventID=1 o tendríamos que traerlo de la DB.
		//  Para no romperte el código, vamos a asumir que todos compran del evento 1 por ahora,
		//  o idealmente GetOrder debería devolver el objeto Orden completo).

		// Asumiendo que podemos obtener la posición:
		// En un caso real, haríamos: order, _ := repo.GetFullOrder(orderID) -> eventID
		pos, _ := h.repo.GetQueuePosition(c.Request.Context(), orderID, 1) // Hardcodeamos 1 por simplicidad del demo
		position = pos
		mensaje = "Estás en la fila virtual."

	case "confirmada":
		mensaje = "🎟️ ¡Felicidades! Tienes tu entrada."
	case "rechazada":
		mensaje = "⛔ Lo sentimos, se agotó el stock o hubo un error."
	}

	c.JSON(http.StatusOK, gin.H{
		"order_id": orderID,
		"status":   estado,
		"mensaje":  mensaje,
		"position": position, // <--- Dato nuevo para el Frontend
	})
}

// GetEvents: GET /events (Lista todos los eventos para la Home)
func (h *Handler) GetEvents(c *gin.Context) {
	eventos, err := h.repo.GetAllEvents(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "error fetching events"})
		return
	}
	c.JSON(http.StatusOK, eventos)
}
