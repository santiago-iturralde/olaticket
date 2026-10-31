package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"stock-api/internal/domain"
	"stock-api/internal/platform/auth"
)

// Límites de las columnas: eventos.nombre VARCHAR(100), sectores.nombre VARCHAR(60),
// sectores.precio DECIMAL(10,2). El resto son topes de negocio.
const (
	maxEventNameLength  = 100
	maxSectorNameLength = 60
	maxEventPrice       = 99_999_999.99
	maxSectores         = 20
	maxSectorCapacity   = 1_000_000
	maxLimitePorOrden   = 20

	maxImageBytes   = 5 << 20                 // Por imagen
	maxUploadBytes  = 2*maxImageBytes + 1<<20 // Portada + mapa + datos del formulario
	maxMemoryUpload = 1 << 20                 // Lo que excede se bufferea en disco
)

// allowedImageTypes son los formatos aceptados, detectados por contenido (no por extensión).
var allowedImageTypes = map[string]bool{"image/jpeg": true, "image/png": true, "image/webp": true}

// eventAdminStore es lo que el backoffice necesita del repositorio.
type eventAdminStore interface {
	GetByID(ctx context.Context, id int64) (*domain.Event, error)
	GetEventsByOrganizador(ctx context.Context, organizadorID int64) ([]domain.EventSummary, error)
	GetOrganizadores(ctx context.Context) ([]domain.Organizador, error)
	CreateEvent(ctx context.Context, e domain.Event) (int64, error)
	UpdateEvent(ctx context.Context, id, organizadorID int64, upd domain.EventUpdate) error
	DeleteEvent(ctx context.Context, id, organizadorID int64) ([]int64, error)
	ScanTicket(ctx context.Context, ticketID, eventID int64) (*domain.ScanResult, error)
}

// orderNotifier avisa a los compradores cuyas órdenes se cancelaron (SSE + limpieza en Redis).
type orderNotifier interface {
	NotifyCancelled(ctx context.Context, orderID int64, message string)
}

// imageUploader sube las imágenes del evento (Cloudinary) y devuelve su URL pública.
type imageUploader interface {
	Enabled() bool
	Upload(ctx context.Context, file io.Reader, filename string) (string, error)
}

// AdminHandler atiende el backoffice (/api/admin). Las rutas ya pasaron por
// AuthMiddleware y RequireRole(organizador, admin).
type AdminHandler struct {
	repo     eventAdminStore
	notifier orderNotifier
	images   imageUploader
}

func NewAdminHandler(repo eventAdminStore, notifier orderNotifier, images imageUploader) *AdminHandler {
	return &AdminHandler{repo: repo, notifier: notifier, images: images}
}

// eventImages son las imágenes que llegaron en un request multipart.
type eventImages struct {
	portada, mapa             *multipart.FileHeader
	quitarPortada, quitarMapa bool
}

func (imgs eventImages) any() bool {
	return imgs.portada != nil || imgs.mapa != nil
}

// bindEventRequest lee el body del alta/edición. Acepta JSON (como antes) o
// multipart/form-data con los mismos datos en el campo "datos" (JSON) más los archivos
// opcionales "portada" y "mapa"; en edición, "quitar_portada"/"quitar_mapa" = "true" borran
// la imagen actual. Si falla ya deja escrita la respuesta.
func bindEventRequest(c *gin.Context, dst any) (eventImages, bool) {
	var imgs eventImages
	if c.ContentType() != "multipart/form-data" {
		if err := c.ShouldBindJSON(dst); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Datos del evento inválidos"})
			return imgs, false
		}
		return imgs, true
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxUploadBytes)
	if err := c.Request.ParseMultipartForm(maxMemoryUpload); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": fmt.Sprintf("Las imágenes no pueden pesar más de %d MB cada una", maxImageBytes>>20)})
			return imgs, false
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": "Formulario inválido"})
		return imgs, false
	}
	if err := json.Unmarshal([]byte(c.Request.PostFormValue("datos")), dst); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Datos del evento inválidos"})
		return imgs, false
	}

	for _, img := range []struct {
		field string
		dst   **multipart.FileHeader
	}{{"portada", &imgs.portada}, {"mapa", &imgs.mapa}} {
		header, err := c.FormFile(img.field)
		if errors.Is(err, http.ErrMissingFile) {
			continue
		}
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "No se pudo leer la imagen", "field": img.field})
			return imgs, false
		}
		if ferr := validateImage(img.field, header); ferr != nil {
			ferr.respond(c)
			return imgs, false
		}
		*img.dst = header
	}
	imgs.quitarPortada = c.Request.PostFormValue("quitar_portada") == "true"
	imgs.quitarMapa = c.Request.PostFormValue("quitar_mapa") == "true"
	return imgs, true
}

// validateImage controla el tamaño y el formato real (los primeros bytes del archivo).
func validateImage(field string, header *multipart.FileHeader) *fieldError {
	invalid := &fieldError{field, fmt.Sprintf("La imagen debe ser JPG, PNG o WebP de hasta %d MB", maxImageBytes>>20)}
	if header.Size <= 0 || header.Size > maxImageBytes {
		return invalid
	}
	f, err := header.Open()
	if err != nil {
		return invalid
	}
	defer f.Close()
	head := make([]byte, 512)
	n, _ := io.ReadFull(f, head)
	if !allowedImageTypes[http.DetectContentType(head[:n])] {
		return invalid
	}
	return nil
}

// uploadImages sube las imágenes recibidas y devuelve sus URLs ("" = no vino esa imagen).
// Si falla ya deja escrita la respuesta.
func (h *AdminHandler) uploadImages(c *gin.Context, imgs eventImages) (portadaURL, mapaURL string, ok bool) {
	if !imgs.any() {
		return "", "", true
	}
	if h.images == nil || !h.images.Enabled() {
		field := "portada"
		if imgs.portada == nil {
			field = "mapa"
		}
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "La subida de imágenes no está configurada en el servidor (CLOUDINARY_URL)", "field": field})
		return "", "", false
	}
	upload := func(field string, header *multipart.FileHeader) (string, bool) {
		if header == nil {
			return "", true
		}
		f, err := header.Open()
		if err == nil {
			defer f.Close()
			var url string
			if url, err = h.images.Upload(c.Request.Context(), f, header.Filename); err == nil {
				return url, true
			}
		}
		log.Printf("Error subiendo la imagen %s a Cloudinary: %v", field, err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "No se pudo subir la imagen. Probá de nuevo en unos minutos", "field": field})
		return "", false
	}
	if portadaURL, ok = upload("portada", imgs.portada); !ok {
		return "", "", false
	}
	if mapaURL, ok = upload("mapa", imgs.mapa); !ok {
		return "", "", false
	}
	return portadaURL, mapaURL, true
}

type sectorRequest struct {
	ID        int64   `json:"id"` // Solo en edición: 0 = sector nuevo
	Nombre    string  `json:"nombre"`
	Precio    float64 `json:"precio"`
	Capacidad int     `json:"capacidad"`
}

type createEventRequest struct {
	Nombre                string          `json:"nombre"`
	LimiteTicketsPorOrden *int            `json:"limite_tickets_por_orden"` // Ausente = DefaultLimiteTicketsPorOrden
	Sectores              []sectorRequest `json:"sectores"`
	OrganizadorID         int64           `json:"organizador_id"` // Obligatorio para admin; un organizador siempre crea para su productora
}

// updateEventRequest: los campos ausentes no se modifican. Sectores, si viene, es la lista completa.
type updateEventRequest struct {
	Nombre                *string         `json:"nombre"`
	LimiteTicketsPorOrden *int            `json:"limite_tickets_por_orden"`
	Sectores              []sectorRequest `json:"sectores"`
}

// respond contesta 400 con el error de validación (fieldError está en auth_handler.go).
// Los campos de sectores se nombran "sectores.<índice>.<campo>".
func (e *fieldError) respond(c *gin.Context) {
	c.JSON(http.StatusBadRequest, gin.H{"error": e.Message, "field": e.Field})
}

func sectorField(i int, name string) string {
	return fmt.Sprintf("sectores.%d.%s", i, name)
}

// validateSectores normaliza los nombres y controla la lista completa de sectores.
// allowIDs es false en el alta, donde todavía no existen sectores que editar.
func validateSectores(reqs []sectorRequest, allowIDs bool) ([]domain.SectorInput, *fieldError) {
	if len(reqs) == 0 {
		return nil, &fieldError{"sectores", "Agregá al menos un sector"}
	}
	if len(reqs) > maxSectores {
		return nil, &fieldError{"sectores", fmt.Sprintf("Máximo %d sectores por evento", maxSectores)}
	}
	sectores := make([]domain.SectorInput, len(reqs))
	nombres := make(map[string]bool, len(reqs))
	ids := make(map[int64]bool, len(reqs))
	for i, req := range reqs {
		nombre := strings.TrimSpace(req.Nombre)
		switch {
		case nombre == "" || utf8.RuneCountInString(nombre) > maxSectorNameLength:
			return nil, &fieldError{sectorField(i, "nombre"), fmt.Sprintf("El nombre del sector es obligatorio (máximo %d caracteres)", maxSectorNameLength)}
		case nombres[strings.ToLower(nombre)]:
			return nil, &fieldError{sectorField(i, "nombre"), "Ya hay otro sector con ese nombre"}
		case !validEventPrice(req.Precio):
			return nil, &fieldError{sectorField(i, "precio"), "El precio debe ser mayor a cero"}
		case req.Capacidad <= 0 || req.Capacidad > maxSectorCapacity:
			return nil, &fieldError{sectorField(i, "capacidad"), "La capacidad debe ser un entero mayor a cero"}
		case req.ID < 0 || (req.ID > 0 && (!allowIDs || ids[req.ID])):
			return nil, &fieldError{sectorField(i, "id"), "Sector inválido"}
		}
		nombres[strings.ToLower(nombre)] = true
		ids[req.ID] = true
		sectores[i] = domain.SectorInput{ID: req.ID, Nombre: nombre, Precio: req.Precio, Capacidad: req.Capacidad}
	}
	return sectores, nil
}

func validLimitePorOrden(limite int) bool {
	return limite >= 1 && limite <= maxLimitePorOrden
}

var errLimitePorOrden = &fieldError{
	"limite_tickets_por_orden",
	fmt.Sprintf("El límite por orden debe estar entre 1 y %d entradas", maxLimitePorOrden),
}

// organizadorScope devuelve a qué productora se limita la operación: 0 para un admin (todas),
// la del usuario para un organizador o portero. Si no puede resolverlo ya deja escrita la respuesta.
func organizadorScope(c *gin.Context) (int64, bool) {
	if c.GetString(auth.RolContextKey) == domain.RolAdmin {
		return 0, true
	}
	organizadorID := c.GetInt64(auth.OrganizadorIDContextKey)
	if organizadorID <= 0 {
		c.JSON(http.StatusForbidden, gin.H{"error": "Tu usuario no tiene una productora asignada"})
		return 0, false
	}
	return organizadorID, true
}

func validEventName(nombre string) bool {
	return nombre != "" && utf8.RuneCountInString(nombre) <= maxEventNameLength
}

func validEventPrice(precio float64) bool {
	return precio > 0 && precio <= maxEventPrice
}

// ListEvents: GET /api/admin/eventos (el organizador ve solo los suyos; el admin, todos)
func (h *AdminHandler) ListEvents(c *gin.Context) {
	scope, ok := organizadorScope(c)
	if !ok {
		return
	}
	eventos, err := h.repo.GetEventsByOrganizador(c.Request.Context(), scope)
	if err != nil {
		log.Printf("Error listing admin events: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "No se pudieron cargar los eventos"})
		return
	}
	if eventos == nil {
		eventos = []domain.EventSummary{} // [] y no null para el frontend
	}
	c.JSON(http.StatusOK, eventos)
}

// GetEvent: GET /api/admin/eventos/:id (para precargar el formulario de edición)
func (h *AdminHandler) GetEvent(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ID de evento inválido"})
		return
	}
	scope, ok := organizadorScope(c)
	if !ok {
		return
	}
	event, err := h.repo.GetByID(c.Request.Context(), id)
	if errors.Is(err, domain.ErrEventNotFound) || (err == nil && scope != 0 && event.OrganizadorID != scope) {
		c.JSON(http.StatusNotFound, gin.H{"error": "Evento no encontrado"})
		return
	}
	if err != nil {
		log.Printf("Error fetching admin event %d: %v", id, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "No se pudo cargar el evento"})
		return
	}
	c.JSON(http.StatusOK, event)
}

// ListOrganizadores: GET /api/admin/organizadores (solo admin: elige la productora al crear)
func (h *AdminHandler) ListOrganizadores(c *gin.Context) {
	organizadores, err := h.repo.GetOrganizadores(c.Request.Context())
	if err != nil {
		log.Printf("Error listing organizadores: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "No se pudieron cargar las productoras"})
		return
	}
	if organizadores == nil {
		organizadores = []domain.Organizador{}
	}
	c.JSON(http.StatusOK, organizadores)
}

// CreateEvent: POST /api/admin/eventos
func (h *AdminHandler) CreateEvent(c *gin.Context) {
	var req createEventRequest
	imgs, ok := bindEventRequest(c, &req)
	if !ok {
		return
	}

	event := domain.Event{
		Nombre:                strings.TrimSpace(req.Nombre),
		LimiteTicketsPorOrden: domain.DefaultLimiteTicketsPorOrden,
	}
	if !validEventName(event.Nombre) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "El nombre es obligatorio (máximo 100 caracteres)", "field": "nombre"})
		return
	}
	if req.LimiteTicketsPorOrden != nil {
		if !validLimitePorOrden(*req.LimiteTicketsPorOrden) {
			errLimitePorOrden.respond(c)
			return
		}
		event.LimiteTicketsPorOrden = *req.LimiteTicketsPorOrden
	}
	sectores, ferr := validateSectores(req.Sectores, false)
	if ferr != nil {
		ferr.respond(c)
		return
	}
	for _, s := range sectores {
		event.Sectores = append(event.Sectores, domain.Sector{Nombre: s.Nombre, Precio: s.Precio, Capacidad: s.Capacidad})
	}

	scope, ok := organizadorScope(c)
	if !ok {
		return
	}
	event.OrganizadorID = scope
	if scope == 0 { // admin: elige el organizador
		if req.OrganizadorID <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Indicá el organizador del evento", "field": "organizador_id"})
			return
		}
		event.OrganizadorID = req.OrganizadorID
	}

	// Con todo validado: recién ahora subimos las imágenes.
	if event.PortadaURL, event.MapaURL, ok = h.uploadImages(c, imgs); !ok {
		return
	}

	id, err := h.repo.CreateEvent(c.Request.Context(), event)
	if err != nil {
		if errors.Is(err, domain.ErrOrganizadorNotFound) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "El organizador no existe", "field": "organizador_id"})
			return
		}
		log.Printf("Error creating event: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "No se pudo crear el evento"})
		return
	}
	h.respondWithEvent(c, id, http.StatusCreated)
}

// UpdateEvent: PUT /api/admin/eventos/:id
func (h *AdminHandler) UpdateEvent(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ID de evento inválido"})
		return
	}

	var req updateEventRequest
	imgs, ok := bindEventRequest(c, &req)
	if !ok {
		return
	}
	if req.Nombre == nil && req.LimiteTicketsPorOrden == nil && req.Sectores == nil &&
		!imgs.any() && !imgs.quitarPortada && !imgs.quitarMapa {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No hay cambios para aplicar"})
		return
	}
	upd := domain.EventUpdate{LimiteTicketsPorOrden: req.LimiteTicketsPorOrden}
	if req.Nombre != nil {
		nombre := strings.TrimSpace(*req.Nombre)
		if !validEventName(nombre) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "El nombre es obligatorio (máximo 100 caracteres)", "field": "nombre"})
			return
		}
		upd.Nombre = &nombre
	}
	if req.LimiteTicketsPorOrden != nil && !validLimitePorOrden(*req.LimiteTicketsPorOrden) {
		errLimitePorOrden.respond(c)
		return
	}
	if req.Sectores != nil {
		sectores, ferr := validateSectores(req.Sectores, true)
		if ferr != nil {
			ferr.respond(c)
			return
		}
		upd.Sectores = sectores
	}

	scope, ok := organizadorScope(c)
	if !ok {
		return
	}

	if imgs.any() {
		// No subimos imágenes para un evento inexistente o ajeno.
		event, err := h.repo.GetByID(c.Request.Context(), id)
		if errors.Is(err, domain.ErrEventNotFound) || (err == nil && scope != 0 && event.OrganizadorID != scope) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Evento no encontrado"})
			return
		}
		if err != nil {
			log.Printf("Error fetching event %d before image upload: %v", id, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "No se pudo actualizar el evento"})
			return
		}
	}
	portadaURL, mapaURL, ok := h.uploadImages(c, imgs)
	if !ok {
		return
	}
	// Imagen nueva > quitar > sin cambios.
	if portadaURL != "" || imgs.quitarPortada {
		upd.PortadaURL = &portadaURL
	}
	if mapaURL != "" || imgs.quitarMapa {
		upd.MapaURL = &mapaURL
	}

	err = h.repo.UpdateEvent(c.Request.Context(), id, scope, upd)
	var sectorErr *domain.SectorError
	switch {
	case err == nil:
		h.respondWithEvent(c, id, http.StatusOK)
	case errors.Is(err, domain.ErrEventNotFound):
		// Un evento de otro organizador también cae acá: no revelamos que existe.
		c.JSON(http.StatusNotFound, gin.H{"error": "Evento no encontrado"})
	case errors.As(err, &sectorErr) && errors.Is(err, domain.ErrTotalBelowSold):
		c.JSON(http.StatusConflict, gin.H{
			"error": "La capacidad no puede ser menor a las entradas ya vendidas o reservadas",
			"field": sectorField(sectorErr.Index, "capacidad"),
		})
	case errors.As(err, &sectorErr) && errors.Is(err, domain.ErrSectorHasSales):
		c.JSON(http.StatusConflict, gin.H{
			"error": fmt.Sprintf("No se puede quitar el sector \"%s\": tiene entradas vendidas o reservadas", sectorErr.Nombre),
			"field": "sectores",
		})
	case errors.As(err, &sectorErr) && errors.Is(err, domain.ErrSectorNotFound):
		// Otro usuario lo eliminó mientras se editaba: hay que recargar el formulario.
		c.JSON(http.StatusConflict, gin.H{"error": "Uno de los sectores ya no existe. Recargá el evento e intentá de nuevo"})
	default:
		log.Printf("Error updating event %d: %v", id, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "No se pudo actualizar el evento"})
	}
}

// DeleteEvent: DELETE /api/admin/eventos/:id
// Solo se pueden eliminar eventos sin entradas pagadas; las reservas en curso se cancelan.
func (h *AdminHandler) DeleteEvent(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ID de evento inválido"})
		return
	}
	scope, ok := organizadorScope(c)
	if !ok {
		return
	}

	cancelled, err := h.repo.DeleteEvent(c.Request.Context(), id, scope)
	switch {
	case errors.Is(err, domain.ErrEventNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "Evento no encontrado"})
		return
	case errors.Is(err, domain.ErrEventHasSales):
		c.JSON(http.StatusConflict, gin.H{"error": "No se puede eliminar un evento con entradas vendidas"})
		return
	case err != nil:
		log.Printf("Error deleting event %d: %v", id, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "No se pudo eliminar el evento"})
		return
	}

	// El borrado ya se confirmó: los avisos salen aunque el cliente corte la conexión.
	notifyCtx := context.WithoutCancel(c.Request.Context())
	for _, orderID := range cancelled {
		h.notifier.NotifyCancelled(notifyCtx, orderID, "El organizador canceló este evento. Tu reserva fue anulada y no se realizó ningún cobro.")
	}
	if len(cancelled) > 0 {
		log.Printf("🗑️  Evento %d eliminado: %d órdenes en curso canceladas", id, len(cancelled))
	}
	c.Status(http.StatusNoContent)
}

type scannerEvent struct {
	ID          int64  `json:"id"`
	Nombre      string `json:"nombre"`
	Organizador string `json:"organizador"`
}

// ScannerEvents: GET /api/admin/escaner/eventos
// Los eventos que puede escanear el usuario (los de su productora; todos para el admin).
// Solo nombre e ID: el portero no ve ventas ni recaudación.
func (h *AdminHandler) ScannerEvents(c *gin.Context) {
	scope, ok := organizadorScope(c)
	if !ok {
		return
	}
	eventos, err := h.repo.GetEventsByOrganizador(c.Request.Context(), scope)
	if err != nil {
		log.Printf("Error listing scanner events: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "No se pudieron cargar los eventos"})
		return
	}
	out := make([]scannerEvent, len(eventos))
	for i, e := range eventos {
		out[i] = scannerEvent{ID: e.ID, Nombre: e.Nombre, Organizador: e.Organizador.Nombre}
	}
	c.JSON(http.StatusOK, out)
}

type scanRequest struct {
	Codigo string `json:"codigo"` // Contenido del QR: "OLT-<ticket_id>-<firma>"
}

// ScanTicket: POST /api/admin/eventos/:id/scan {codigo}
// Control de acceso en la puerta. "resultado" resume lo que tiene que ver el personal:
//   - 200 permitido: primera vez que pasa la entrada (queda ingresada).
//   - 409 ya_ingreso: la entrada ya se usó (se informa cuándo).
//   - 403 no_pagada: la orden no está confirmada.
//   - 404 invalido: QR que no es de Olaticket, adulterado, o de otro evento.
//
// Las entradas válidas devuelven nombre y DNI del titular para controlar el documento.
func (h *AdminHandler) ScanTicket(c *gin.Context) {
	eventID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || eventID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ID de evento inválido"})
		return
	}
	scope, ok := organizadorScope(c)
	if !ok {
		return
	}
	event, err := h.repo.GetByID(c.Request.Context(), eventID)
	if errors.Is(err, domain.ErrEventNotFound) || (err == nil && scope != 0 && event.OrganizadorID != scope) {
		c.JSON(http.StatusNotFound, gin.H{"error": "Evento no encontrado"})
		return
	}
	if err != nil {
		log.Printf("Error fetching event %d to scan: %v", eventID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "No se pudo validar la entrada"})
		return
	}

	var req scanRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Codigo == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Falta el código del QR"})
		return
	}
	invalid := func(msg string) {
		c.JSON(http.StatusNotFound, gin.H{"resultado": "invalido", "error": msg})
	}
	ticketID, err := auth.ParseEntryCode(req.Codigo)
	if err != nil {
		invalid("El QR no es una entrada válida de Olaticket")
		return
	}

	res, err := h.repo.ScanTicket(c.Request.Context(), ticketID, eventID)
	switch {
	case err == nil:
		c.JSON(http.StatusOK, gin.H{"resultado": "permitido", "mensaje": "Adelante", "ticket": res.Ticket})
	case errors.Is(err, domain.ErrTicketNotFound):
		invalid("La entrada no existe")
	case errors.Is(err, domain.ErrTicketOtherEvent):
		invalid("La entrada es de otro evento")
	case errors.Is(err, domain.ErrTicketNotPaid):
		c.JSON(http.StatusForbidden, gin.H{"resultado": "no_pagada", "error": "La compra de esta entrada no está pagada", "ticket": res.Ticket})
	case errors.Is(err, domain.ErrTicketAlreadyUsed):
		c.JSON(http.StatusConflict, gin.H{"resultado": "ya_ingreso", "error": "Esta entrada ya ingresó", "ticket": res.Ticket})
	default:
		log.Printf("Error scanning ticket %d for event %d: %v", ticketID, eventID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "No se pudo validar la entrada"})
	}
}

func (h *AdminHandler) respondWithEvent(c *gin.Context, id int64, status int) {
	event, err := h.repo.GetByID(c.Request.Context(), id)
	if err != nil {
		log.Printf("Error fetching event %d after admin change: %v", id, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "El evento se guardó pero no se pudo leer"})
		return
	}
	c.JSON(status, event)
}
