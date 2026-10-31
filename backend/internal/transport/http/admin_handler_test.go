package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"stock-api/internal/domain"
	"stock-api/internal/platform/auth"
)

// fakeEventStore imita las reglas de UpdateEvent del repositorio MySQL.
type fakeEventStore struct {
	events  map[int64]*domain.Event
	created domain.Event

	sold     map[int64]int     // entradas pagadas por evento
	revenue  map[int64]float64 // recaudación por evento
	inFlight map[int64][]int64 // órdenes pendientes/reservadas por evento
	notified []int64           // órdenes avisadas vía NotifyCancelled

	nextSectorID int64

	tickets map[int64]*fakeTicket // entradas para el escáner
}

func newEvent(id int64, nombre string, organizadorID int64, sectores ...domain.Sector) *domain.Event {
	e := &domain.Event{ID: id, Nombre: nombre, LimiteTicketsPorOrden: 4, OrganizadorID: organizadorID}
	for i := range sectores {
		sectores[i].EventoID = id
	}
	e.SetSectores(sectores)
	return e
}

func newFakeEventStore() *fakeEventStore {
	return &fakeEventStore{
		events: map[int64]*domain.Event{
			// Evento 1: General 100 de capacidad con 60 vendidas/reservadas; VIP sin ventas.
			1: newEvent(1, "Evento A", 1,
				domain.Sector{ID: 10, Nombre: "General", Precio: 1000, Capacidad: 100, StockDisponible: 40},
				domain.Sector{ID: 11, Nombre: "VIP", Precio: 3000, Capacidad: 20, StockDisponible: 20}),
			2: newEvent(2, "Evento B", 2,
				domain.Sector{ID: 20, Nombre: "General", Precio: 2000, Capacidad: 50, StockDisponible: 50}),
		},
		sold:         map[int64]int{},
		revenue:      map[int64]float64{},
		inFlight:     map[int64][]int64{},
		nextSectorID: 100,
	}
}

func (s *fakeEventStore) GetByID(_ context.Context, id int64) (*domain.Event, error) {
	if e, ok := s.events[id]; ok {
		return e, nil
	}
	return nil, domain.ErrEventNotFound
}

func (s *fakeEventStore) GetEventsByOrganizador(_ context.Context, organizadorID int64) ([]domain.EventSummary, error) {
	var eventos []domain.EventSummary
	for id := int64(1); id <= 99; id++ {
		if e, ok := s.events[id]; ok && (organizadorID == 0 || e.OrganizadorID == organizadorID) {
			eventos = append(eventos, domain.EventSummary{Event: *e, EntradasVendidas: s.sold[id], Recaudacion: s.revenue[id]})
		}
	}
	return eventos, nil
}

func (s *fakeEventStore) DeleteEvent(_ context.Context, id, organizadorID int64) ([]int64, error) {
	e, ok := s.events[id]
	if !ok || (organizadorID != 0 && e.OrganizadorID != organizadorID) {
		return nil, domain.ErrEventNotFound
	}
	if s.sold[id] > 0 {
		return nil, domain.ErrEventHasSales
	}
	delete(s.events, id)
	return s.inFlight[id], nil
}

func (s *fakeEventStore) NotifyCancelled(_ context.Context, orderID int64, _ string) {
	s.notified = append(s.notified, orderID)
}

func (s *fakeEventStore) GetOrganizadores(context.Context) ([]domain.Organizador, error) {
	return []domain.Organizador{{ID: 1, Nombre: "Olaticket Producciones"}, {ID: 2, Nombre: "Teatro"}}, nil
}

func (s *fakeEventStore) CreateEvent(_ context.Context, e domain.Event) (int64, error) {
	if e.OrganizadorID != 1 && e.OrganizadorID != 2 {
		return 0, domain.ErrOrganizadorNotFound
	}
	s.created = e
	sectores := make([]domain.Sector, len(e.Sectores))
	for i, sec := range e.Sectores {
		s.nextSectorID++
		sec.ID, sec.StockDisponible = s.nextSectorID, sec.Capacidad
		sectores[i] = sec
	}
	stored := newEvent(99, e.Nombre, e.OrganizadorID, sectores...)
	stored.LimiteTicketsPorOrden = e.LimiteTicketsPorOrden
	stored.PortadaURL, stored.MapaURL = e.PortadaURL, e.MapaURL
	s.events[99] = stored
	return 99, nil
}

func (s *fakeEventStore) UpdateEvent(_ context.Context, id, organizadorID int64, upd domain.EventUpdate) error {
	e, ok := s.events[id]
	if !ok || (organizadorID != 0 && e.OrganizadorID != organizadorID) {
		return domain.ErrEventNotFound
	}
	if upd.Sectores != nil {
		actuales := map[int64]domain.Sector{}
		for _, sec := range e.Sectores {
			actuales[sec.ID] = sec
		}
		var sectores []domain.Sector
		for i, in := range upd.Sectores {
			sec := domain.Sector{ID: in.ID, EventoID: id, Nombre: in.Nombre, Precio: in.Precio, Capacidad: in.Capacidad, StockDisponible: in.Capacidad}
			if in.ID == 0 {
				s.nextSectorID++
				sec.ID = s.nextSectorID
			} else {
				actual, ok := actuales[in.ID]
				if !ok {
					return &domain.SectorError{Index: i, Nombre: in.Nombre, Err: domain.ErrSectorNotFound}
				}
				delete(actuales, in.ID)
				sec.StockDisponible = actual.StockDisponible + in.Capacidad - actual.Capacidad
				if sec.StockDisponible < 0 {
					return &domain.SectorError{Index: i, Nombre: in.Nombre, Err: domain.ErrTotalBelowSold}
				}
			}
			sectores = append(sectores, sec)
		}
		for _, sec := range actuales {
			if sec.StockDisponible != sec.Capacidad {
				return &domain.SectorError{Index: -1, Nombre: sec.Nombre, Err: domain.ErrSectorHasSales}
			}
		}
		e.SetSectores(sectores)
	}
	if upd.LimiteTicketsPorOrden != nil {
		e.LimiteTicketsPorOrden = *upd.LimiteTicketsPorOrden
	}
	if upd.Nombre != nil {
		e.Nombre = *upd.Nombre
	}
	if upd.PortadaURL != nil {
		e.PortadaURL = *upd.PortadaURL
	}
	if upd.MapaURL != nil {
		e.MapaURL = *upd.MapaURL
	}
	return nil
}

// fakeUploader imita Cloudinary: devuelve una URL con el nombre del archivo.
type fakeUploader struct {
	disabled bool
	fail     bool
	uploads  []string
}

func (u *fakeUploader) Enabled() bool { return !u.disabled }

func (u *fakeUploader) Upload(_ context.Context, file io.Reader, filename string) (string, error) {
	if u.fail {
		return "", errors.New("cloudinary caído")
	}
	if _, err := io.ReadAll(file); err != nil {
		return "", err
	}
	u.uploads = append(u.uploads, filename)
	return "https://res.cloudinary.com/demo/" + filename, nil
}

// adminRouter simula lo que AuthMiddleware deja en el contexto.
func adminRouter(store *fakeEventStore, rol string, organizadorID int64) *gin.Engine {
	return adminRouterWithImages(store, &fakeUploader{}, rol, organizadorID)
}

func adminRouterWithImages(store *fakeEventStore, images *fakeUploader, rol string, organizadorID int64) *gin.Engine {
	gin.SetMode(gin.TestMode)
	h := NewAdminHandler(store, store, images)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(auth.RolContextKey, rol)
		c.Set(auth.OrganizadorIDContextKey, organizadorID)
	})
	r.GET("/api/admin/eventos", h.ListEvents)
	r.GET("/api/admin/eventos/:id", h.GetEvent)
	r.POST("/api/admin/eventos", h.CreateEvent)
	r.PUT("/api/admin/eventos/:id", h.UpdateEvent)
	r.DELETE("/api/admin/eventos/:id", h.DeleteEvent)
	return r
}

func doJSON(r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	return res
}

// pngBytes es un PNG mínimo (alcanza la firma para que DetectContentType lo reconozca).
var pngBytes = append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 64)...)

// doMultipart manda "datos" (JSON) más los archivos indicados (campo -> contenido) y campos extra.
func doMultipart(r *gin.Engine, method, path, datos string, files map[string][]byte, fields map[string]string) *httptest.ResponseRecorder {
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	form.WriteField("datos", datos)
	for k, v := range fields {
		form.WriteField(k, v)
	}
	for field, content := range files {
		part, _ := form.CreateFormFile(field, field+".png")
		part.Write(content)
	}
	form.Close()
	req := httptest.NewRequest(method, path, &body)
	req.Header.Set("Content-Type", form.FormDataContentType())
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	return res
}

// errorField devuelve el "field" de una respuesta de error.
func errorField(t *testing.T, res *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Field string `json:"field"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatalf("respuesta inválida: %v (%s)", err, res.Body)
	}
	return body.Field
}

const unSector = `"sectores":[{"nombre":"General","precio":5000,"capacidad":300}]`

func TestAdminCreateEventOrganizadorUsesOwnTenant(t *testing.T) {
	store := newFakeEventStore()
	r := adminRouter(store, domain.RolOrganizador, 2)

	// El organizador_id del body se ignora: siempre crea para su productora.
	res := doJSON(r, http.MethodPost, "/api/admin/eventos",
		`{"nombre":" Recital ",`+unSector+`,"organizador_id":1}`)
	if res.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", res.Code, res.Body)
	}
	if store.created.OrganizadorID != 2 || store.created.Nombre != "Recital" {
		t.Fatalf("created = %+v, want organizador 2 y nombre recortado", store.created)
	}
}

func TestAdminCreateEventWithSectores(t *testing.T) {
	store := newFakeEventStore()
	r := adminRouter(store, domain.RolOrganizador, 1)

	res := doJSON(r, http.MethodPost, "/api/admin/eventos", `{
		"nombre":"Recital","limite_tickets_por_orden":6,
		"sectores":[{"nombre":" General ","precio":5000,"capacidad":300},{"nombre":"Platea VIP","precio":12000.5,"capacidad":50}]
	}`)
	if res.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", res.Code, res.Body)
	}
	c := store.created
	if c.LimiteTicketsPorOrden != 6 || len(c.Sectores) != 2 ||
		c.Sectores[0].Nombre != "General" || c.Sectores[1].Precio != 12000.5 || c.Sectores[1].Capacidad != 50 {
		t.Fatalf("created = %+v", c)
	}

	var got domain.Event
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatalf("respuesta inválida: %v", err)
	}
	if got.TotalTickets != 350 || got.StockDisponible != 350 || len(got.Sectores) != 2 {
		t.Fatalf("respuesta = %+v, want totales sumados de los sectores", got)
	}
}

func TestAdminCreateEventDefaultLimit(t *testing.T) {
	store := newFakeEventStore()
	r := adminRouter(store, domain.RolOrganizador, 1)

	if res := doJSON(r, http.MethodPost, "/api/admin/eventos", `{"nombre":"Recital",`+unSector+`}`); res.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", res.Code, res.Body)
	}
	if store.created.LimiteTicketsPorOrden != domain.DefaultLimiteTicketsPorOrden {
		t.Fatalf("límite = %d, want %d", store.created.LimiteTicketsPorOrden, domain.DefaultLimiteTicketsPorOrden)
	}
}

func TestAdminCreateEventAdminMustChooseOrganizador(t *testing.T) {
	store := newFakeEventStore()
	r := adminRouter(store, domain.RolAdmin, 0)

	if res := doJSON(r, http.MethodPost, "/api/admin/eventos",
		`{"nombre":"Recital",`+unSector+`}`); res.Code != http.StatusBadRequest {
		t.Fatalf("sin organizador_id: status = %d, want 400", res.Code)
	}
	if res := doJSON(r, http.MethodPost, "/api/admin/eventos",
		`{"nombre":"Recital",`+unSector+`,"organizador_id":7}`); res.Code != http.StatusBadRequest {
		t.Fatalf("organizador inexistente: status = %d, want 400", res.Code)
	}
	if res := doJSON(r, http.MethodPost, "/api/admin/eventos",
		`{"nombre":"Recital",`+unSector+`,"organizador_id":1}`); res.Code != http.StatusCreated {
		t.Fatalf("admin válido: status = %d, body = %s", res.Code, res.Body)
	}
}

func TestAdminCreateEventValidation(t *testing.T) {
	r := adminRouter(newFakeEventStore(), domain.RolAdmin, 0)
	sector := func(nombre string, precio, capacidad string) string {
		return `{"nombre":"` + nombre + `","precio":` + precio + `,"capacidad":` + capacidad + `}`
	}
	for name, tc := range map[string]struct{ body, field string }{
		"nombre vacío":     {`{"nombre":"  ",` + unSector + `,"organizador_id":1}`, "nombre"},
		"nombre largo":     {`{"nombre":"` + strings.Repeat("x", 101) + `",` + unSector + `,"organizador_id":1}`, "nombre"},
		"sin sectores":     {`{"nombre":"A","sectores":[],"organizador_id":1}`, "sectores"},
		"sectores ausente": {`{"nombre":"A","organizador_id":1}`, "sectores"},
		"sector sin nombre": {`{"nombre":"A","sectores":[` + sector("General", "100", "10") + `,` + sector(" ", "100", "10") + `],"organizador_id":1}`,
			"sectores.1.nombre"},
		"sector repetido": {`{"nombre":"A","sectores":[` + sector("VIP", "100", "10") + `,` + sector("vip", "200", "10") + `],"organizador_id":1}`,
			"sectores.1.nombre"},
		"precio cero":     {`{"nombre":"A","sectores":[` + sector("General", "0", "10") + `],"organizador_id":1}`, "sectores.0.precio"},
		"capacidad cero":  {`{"nombre":"A","sectores":[` + sector("General", "100", "0") + `],"organizador_id":1}`, "sectores.0.capacidad"},
		"id en el alta":   {`{"nombre":"A","sectores":[{"id":10,"nombre":"G","precio":1,"capacidad":1}],"organizador_id":1}`, "sectores.0.id"},
		"límite cero":     {`{"nombre":"A",` + unSector + `,"limite_tickets_por_orden":0,"organizador_id":1}`, "limite_tickets_por_orden"},
		"límite excesivo": {`{"nombre":"A",` + unSector + `,"limite_tickets_por_orden":21,"organizador_id":1}`, "limite_tickets_por_orden"},
		"JSON inválido":   {`{"nombre":`, ""},
		"tipo incorrecto": {`{"nombre":"A","sectores":[{"nombre":"G","precio":1,"capacidad":"diez"}],"organizador_id":1}`, ""},
	} {
		res := doJSON(r, http.MethodPost, "/api/admin/eventos", tc.body)
		if res.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", name, res.Code)
			continue
		}
		if field := errorField(t, res); field != tc.field {
			t.Errorf("%s: field = %q, want %q", name, field, tc.field)
		}
	}
}

func TestAdminOrganizadorWithoutTenantIsForbidden(t *testing.T) {
	r := adminRouter(newFakeEventStore(), domain.RolOrganizador, 0)
	if res := doJSON(r, http.MethodPost, "/api/admin/eventos",
		`{"nombre":"A",`+unSector+`}`); res.Code != http.StatusForbidden {
		t.Fatalf("create: status = %d, want 403", res.Code)
	}
	if res := doJSON(r, http.MethodPut, "/api/admin/eventos/1", `{"nombre":"B"}`); res.Code != http.StatusForbidden {
		t.Fatalf("update: status = %d, want 403", res.Code)
	}
}

func TestAdminUpdateEventTenantIsolation(t *testing.T) {
	store := newFakeEventStore()
	r := adminRouter(store, domain.RolOrganizador, 1)

	// Evento de otro organizador: 404 y sin cambios.
	if res := doJSON(r, http.MethodPut, "/api/admin/eventos/2", `{"nombre":"Hackeado"}`); res.Code != http.StatusNotFound {
		t.Fatalf("evento ajeno: status = %d, want 404", res.Code)
	}
	if store.events[2].Nombre != "Evento B" {
		t.Fatalf("evento ajeno modificado: %+v", store.events[2])
	}

	// Evento propio: se actualiza y responde con el evento.
	res := doJSON(r, http.MethodPut, "/api/admin/eventos/1", `{"limite_tickets_por_orden":2}`)
	if res.Code != http.StatusOK || store.events[1].LimiteTicketsPorOrden != 2 {
		t.Fatalf("evento propio: status = %d, evento = %+v", res.Code, store.events[1])
	}

	// El admin puede editar cualquiera.
	r = adminRouter(store, domain.RolAdmin, 0)
	if res := doJSON(r, http.MethodPut, "/api/admin/eventos/2", `{"nombre":"Evento B2"}`); res.Code != http.StatusOK {
		t.Fatalf("admin: status = %d, want 200", res.Code)
	}
}

func TestAdminUpdateEventSectores(t *testing.T) {
	store := newFakeEventStore() // Evento 1: General (10) 100/40 disponibles, VIP (11) sin ventas
	r := adminRouter(store, domain.RolOrganizador, 1)

	// Amplía General, quita VIP (sin ventas) y agrega Platea.
	res := doJSON(r, http.MethodPut, "/api/admin/eventos/1", `{"sectores":[
		{"id":10,"nombre":"Campo","precio":1500,"capacidad":150},
		{"nombre":"Platea","precio":4000,"capacidad":30}
	]}`)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", res.Code, res.Body)
	}
	e := store.events[1]
	if len(e.Sectores) != 2 {
		t.Fatalf("sectores = %+v", e.Sectores)
	}
	campo, platea := e.Sectores[0], e.Sectores[1]
	if campo.ID != 10 || campo.Nombre != "Campo" || campo.Capacidad != 150 || campo.StockDisponible != 90 {
		t.Fatalf("campo = %+v, want capacidad 150 y stock 90", campo)
	}
	if platea.ID == 0 || platea.StockDisponible != 30 {
		t.Fatalf("platea = %+v", platea)
	}
	if e.TotalTickets != 180 || e.StockDisponible != 120 {
		t.Fatalf("totales = %d/%d, want 180/120", e.TotalTickets, e.StockDisponible)
	}
}

func TestAdminUpdateEventSectorConflicts(t *testing.T) {
	store := newFakeEventStore()
	r := adminRouter(store, domain.RolOrganizador, 1)

	// Capacidad menor a lo vendido/reservado (60): 409 marcando la fila.
	res := doJSON(r, http.MethodPut, "/api/admin/eventos/1", `{"sectores":[
		{"id":11,"nombre":"VIP","precio":3000,"capacidad":20},
		{"id":10,"nombre":"General","precio":1000,"capacidad":59}
	]}`)
	if res.Code != http.StatusConflict || errorField(t, res) != "sectores.1.capacidad" {
		t.Fatalf("capacidad < vendido: status = %d, body = %s", res.Code, res.Body)
	}

	// Quitar un sector con ventas: 409.
	res = doJSON(r, http.MethodPut, "/api/admin/eventos/1", `{"sectores":[{"id":11,"nombre":"VIP","precio":3000,"capacidad":20}]}`)
	if res.Code != http.StatusConflict || !strings.Contains(res.Body.String(), "General") {
		t.Fatalf("quitar sector con ventas: status = %d, body = %s", res.Code, res.Body)
	}

	// Sector de otro evento: 409 sin tocar nada.
	res = doJSON(r, http.MethodPut, "/api/admin/eventos/1", `{"sectores":[
		{"id":10,"nombre":"General","precio":1000,"capacidad":100},
		{"id":20,"nombre":"Ajeno","precio":1,"capacidad":1}
	]}`)
	if res.Code != http.StatusConflict {
		t.Fatalf("sector ajeno: status = %d, body = %s", res.Code, res.Body)
	}
	if len(store.events[1].Sectores) != 2 || store.events[2].Sectores[0].Nombre != "General" {
		t.Fatalf("se modificaron sectores en un update rechazado")
	}
}

func TestAdminUpdateEventValidation(t *testing.T) {
	r := adminRouter(newFakeEventStore(), domain.RolAdmin, 0)
	for name, tc := range map[string]struct{ path, body string }{
		"sin cambios":     {"/api/admin/eventos/1", `{}`},
		"id inválido":     {"/api/admin/eventos/abc", `{"nombre":"B"}`},
		"sectores vacíos": {"/api/admin/eventos/1", `{"sectores":[]}`},
		"id repetido":     {"/api/admin/eventos/1", `{"sectores":[{"id":10,"nombre":"A","precio":1,"capacidad":100},{"id":10,"nombre":"B","precio":1,"capacidad":100}]}`},
		"límite cero":     {"/api/admin/eventos/1", `{"limite_tickets_por_orden":0}`},
		"nombre vacío":    {"/api/admin/eventos/1", `{"nombre":" "}`},
	} {
		if res := doJSON(r, http.MethodPut, tc.path, tc.body); res.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400 (%s)", name, res.Code, res.Body)
		}
	}
	if res := doJSON(r, http.MethodPut, "/api/admin/eventos/404", `{"nombre":"B"}`); res.Code != http.StatusNotFound {
		t.Errorf("inexistente: status = %d, want 404", res.Code)
	}
}

func TestAdminListEventsScopedToTenant(t *testing.T) {
	store := newFakeEventStore()

	res := doJSON(adminRouter(store, domain.RolOrganizador, 2), http.MethodGet, "/api/admin/eventos", "")
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "Evento B") || strings.Contains(res.Body.String(), "Evento A") {
		t.Fatalf("organizador 2: %d %s", res.Code, res.Body)
	}

	res = doJSON(adminRouter(store, domain.RolAdmin, 0), http.MethodGet, "/api/admin/eventos", "")
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "Evento A") || !strings.Contains(res.Body.String(), "Evento B") {
		t.Fatalf("admin: %d %s", res.Code, res.Body)
	}

	// Una productora sin eventos recibe [] (no null).
	res = doJSON(adminRouter(store, domain.RolOrganizador, 3), http.MethodGet, "/api/admin/eventos", "")
	if res.Code != http.StatusOK || strings.TrimSpace(res.Body.String()) != "[]" {
		t.Fatalf("sin eventos: %d %s", res.Code, res.Body)
	}
}

func TestAdminGetEventScopedToTenant(t *testing.T) {
	store := newFakeEventStore()
	r := adminRouter(store, domain.RolOrganizador, 1)

	if res := doJSON(r, http.MethodGet, "/api/admin/eventos/1", ""); res.Code != http.StatusOK {
		t.Fatalf("evento propio: status = %d", res.Code)
	}
	if res := doJSON(r, http.MethodGet, "/api/admin/eventos/2", ""); res.Code != http.StatusNotFound {
		t.Fatalf("evento ajeno: status = %d, want 404", res.Code)
	}
	if res := doJSON(adminRouter(store, domain.RolAdmin, 0), http.MethodGet, "/api/admin/eventos/2", ""); res.Code != http.StatusOK {
		t.Fatalf("admin: status = %d, want 200", res.Code)
	}
}

func TestAdminListEventsIncludesRealSales(t *testing.T) {
	store := newFakeEventStore()
	store.sold[1], store.revenue[1] = 12, 12500.5

	res := doJSON(adminRouter(store, domain.RolOrganizador, 1), http.MethodGet, "/api/admin/eventos", "")
	var got []struct {
		ID               int64           `json:"id"`
		Nombre           string          `json:"nombre"`
		EntradasVendidas int             `json:"entradas_vendidas"`
		Recaudacion      float64         `json:"recaudacion"`
		Sectores         []domain.Sector `json:"sectores"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatalf("respuesta inválida: %v (%s)", err, res.Body)
	}
	if len(got) != 1 || got[0].Nombre != "Evento A" || got[0].EntradasVendidas != 12 || got[0].Recaudacion != 12500.5 || len(got[0].Sectores) != 2 {
		t.Fatalf("eventos = %+v", got)
	}
}

func TestAdminDeleteEvent(t *testing.T) {
	store := newFakeEventStore()
	store.inFlight[1] = []int64{10, 11}
	r := adminRouter(store, domain.RolOrganizador, 1)

	if res := doJSON(r, http.MethodDelete, "/api/admin/eventos/2", ""); res.Code != http.StatusNotFound {
		t.Fatalf("evento ajeno: status = %d, want 404", res.Code)
	}
	if _, ok := store.events[2]; !ok {
		t.Fatal("se eliminó un evento de otra productora")
	}

	if res := doJSON(r, http.MethodDelete, "/api/admin/eventos/1", ""); res.Code != http.StatusNoContent {
		t.Fatalf("evento propio: status = %d, want 204", res.Code)
	}
	if _, ok := store.events[1]; ok {
		t.Fatal("el evento sigue existiendo")
	}
	if len(store.notified) != 2 || store.notified[0] != 10 || store.notified[1] != 11 {
		t.Fatalf("órdenes avisadas = %v, want [10 11]", store.notified)
	}

	if res := doJSON(r, http.MethodDelete, "/api/admin/eventos/abc", ""); res.Code != http.StatusBadRequest {
		t.Fatalf("id inválido: status = %d, want 400", res.Code)
	}
}

func TestAdminDeleteEventWithSalesIsConflict(t *testing.T) {
	store := newFakeEventStore()
	store.sold[2] = 3
	r := adminRouter(store, domain.RolAdmin, 0)

	res := doJSON(r, http.MethodDelete, "/api/admin/eventos/2", "")
	if res.Code != http.StatusConflict || !strings.Contains(res.Body.String(), "entradas vendidas") {
		t.Fatalf("status = %d, body = %s; want 409", res.Code, res.Body)
	}
	if _, ok := store.events[2]; !ok {
		t.Fatal("se eliminó un evento con ventas")
	}
}

func TestAdminCreateEventWithImages(t *testing.T) {
	store := newFakeEventStore()
	images := &fakeUploader{}
	r := adminRouterWithImages(store, images, domain.RolOrganizador, 1)

	res := doMultipart(r, http.MethodPost, "/api/admin/eventos", `{"nombre":"Recital",`+unSector+`}`,
		map[string][]byte{"portada": pngBytes, "mapa": pngBytes}, nil)
	if res.Code != http.StatusCreated {
		t.Fatalf("status = %d (%s)", res.Code, res.Body)
	}
	if store.created.PortadaURL != "https://res.cloudinary.com/demo/portada.png" || store.created.MapaURL != "https://res.cloudinary.com/demo/mapa.png" {
		t.Fatalf("urls = %q %q", store.created.PortadaURL, store.created.MapaURL)
	}
	if store.created.Nombre != "Recital" || len(store.created.Sectores) != 1 {
		t.Fatalf("datos del multipart mal leídos: %+v", store.created)
	}

	// multipart sin archivos también vale
	if res := doMultipart(r, http.MethodPost, "/api/admin/eventos", `{"nombre":"Sin fotos",`+unSector+`}`, nil, nil); res.Code != http.StatusCreated {
		t.Fatalf("sin imágenes: status = %d (%s)", res.Code, res.Body)
	}
}

func TestAdminEventImageErrors(t *testing.T) {
	datos := `{"nombre":"Recital",` + unSector + `}`
	for name, tc := range map[string]struct {
		images    *fakeUploader
		files     map[string][]byte
		datos     string
		wantCode  int
		wantField string
	}{
		"no es imagen":          {&fakeUploader{}, map[string][]byte{"mapa": []byte("%PDF-1.4 hola")}, datos, http.StatusBadRequest, "mapa"},
		"muy pesada":            {&fakeUploader{}, map[string][]byte{"portada": append(pngBytes, make([]byte, maxImageBytes)...)}, datos, http.StatusBadRequest, "portada"},
		"sin cloudinary":        {&fakeUploader{disabled: true}, map[string][]byte{"portada": pngBytes}, datos, http.StatusServiceUnavailable, "portada"},
		"cloudinary falla":      {&fakeUploader{fail: true}, map[string][]byte{"portada": pngBytes}, datos, http.StatusBadGateway, "portada"},
		"datos inválidos":       {&fakeUploader{}, map[string][]byte{"portada": pngBytes}, `{"nombre":`, http.StatusBadRequest, ""},
		"valida antes de subir": {&fakeUploader{}, map[string][]byte{"portada": pngBytes}, `{"nombre":"","sectores":[]}`, http.StatusBadRequest, "nombre"},
	} {
		store := newFakeEventStore()
		r := adminRouterWithImages(store, tc.images, domain.RolOrganizador, 1)
		res := doMultipart(r, http.MethodPost, "/api/admin/eventos", tc.datos, tc.files, nil)
		if res.Code != tc.wantCode {
			t.Errorf("%s: status = %d, want %d (%s)", name, res.Code, tc.wantCode, res.Body)
			continue
		}
		if tc.wantField != "" && errorField(t, res) != tc.wantField {
			t.Errorf("%s: field = %q, want %q", name, errorField(t, res), tc.wantField)
		}
		if tc.wantCode != http.StatusCreated && len(tc.images.uploads) > 0 {
			t.Errorf("%s: se subieron imágenes de un alta rechazada: %v", name, tc.images.uploads)
		}
		if store.created.Nombre != "" {
			t.Errorf("%s: se creó el evento", name)
		}
	}
}

func TestAdminUpdateEventImages(t *testing.T) {
	store := newFakeEventStore()
	store.events[1].PortadaURL = "https://res.cloudinary.com/demo/vieja.png"
	store.events[1].MapaURL = "https://res.cloudinary.com/demo/mapa-viejo.png"
	images := &fakeUploader{}
	r := adminRouterWithImages(store, images, domain.RolOrganizador, 1)

	// Solo una portada nueva y quitar el mapa: el resto no cambia.
	res := doMultipart(r, http.MethodPut, "/api/admin/eventos/1", `{}`,
		map[string][]byte{"portada": pngBytes}, map[string]string{"quitar_mapa": "true"})
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d (%s)", res.Code, res.Body)
	}
	e := store.events[1]
	if e.PortadaURL != "https://res.cloudinary.com/demo/portada.png" || e.MapaURL != "" || e.Nombre != "Evento A" {
		t.Fatalf("evento = %+v", e)
	}

	// Multipart sin cambios -> 400
	if res := doMultipart(r, http.MethodPut, "/api/admin/eventos/1", `{}`, nil, nil); res.Code != http.StatusBadRequest {
		t.Fatalf("sin cambios: status = %d", res.Code)
	}

	// Evento ajeno: 404 y no se sube nada
	images.uploads = nil
	if res := doMultipart(r, http.MethodPut, "/api/admin/eventos/2", `{}`, map[string][]byte{"portada": pngBytes}, nil); res.Code != http.StatusNotFound {
		t.Fatalf("ajeno: status = %d", res.Code)
	}
	if len(images.uploads) > 0 {
		t.Fatalf("se subió una imagen para un evento ajeno")
	}
}

func TestValidatePurchase(t *testing.T) {
	e := newEvent(1, "Evento", 1,
		domain.Sector{ID: 10, Nombre: "General", Precio: 1000, Capacidad: 100, StockDisponible: 3},
		domain.Sector{ID: 11, Nombre: "VIP", Precio: 3000, Capacidad: 20, StockDisponible: 20})
	type it = comprarItem
	for name, tc := range map[string]struct {
		items     []comprarItem
		wantErr   error
		wantIndex int
		want      []domain.OrderItemInput
	}{
		"un sector":             {[]it{{11, 4}}, nil, 0, []domain.OrderItemInput{{SectorID: 11, Cantidad: 4}}},
		"multi-sector ordenado": {[]it{{11, 2}, {10, 2}}, nil, 0, []domain.OrderItemInput{{SectorID: 10, Cantidad: 2}, {SectorID: 11, Cantidad: 2}}},
		"une repetidos":         {[]it{{11, 1}, {10, 1}, {11, 2}}, nil, 0, []domain.OrderItemInput{{SectorID: 10, Cantidad: 1}, {SectorID: 11, Cantidad: 3}}},
		"carrito vacío":         {nil, domain.ErrOverPurchaseLimit, -1, nil},
		"sector de otro":        {[]it{{11, 1}, {20, 1}}, domain.ErrSectorNotFound, 1, nil},
		"cantidad cero":         {[]it{{10, 1}, {11, 0}}, domain.ErrOverPurchaseLimit, 1, nil},
		"línea supera límite":   {[]it{{11, 5}}, domain.ErrOverPurchaseLimit, 0, nil},
		"suma supera límite":    {[]it{{10, 2}, {11, 3}}, domain.ErrOverPurchaseLimit, -1, nil},
		"stock insuficiente":    {[]it{{10, 2}, {10, 2}}, domain.ErrNotEnoughStock, 0, nil},
		"justo el stock":        {[]it{{10, 3}, {11, 1}}, nil, 0, []domain.OrderItemInput{{SectorID: 10, Cantidad: 3}, {SectorID: 11, Cantidad: 1}}},
	} {
		got, perr := validatePurchase(e, tc.items)
		if tc.wantErr != nil {
			if perr == nil || perr.Err != tc.wantErr || perr.Index != tc.wantIndex {
				t.Errorf("%s: err = %+v, want %v en %d", name, perr, tc.wantErr, tc.wantIndex)
			}
			continue
		}
		if perr != nil {
			t.Errorf("%s: err inesperado %+v", name, perr)
			continue
		}
		if len(got) != len(tc.want) {
			t.Errorf("%s: items = %+v, want %+v", name, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%s: items = %+v, want %+v", name, got, tc.want)
				break
			}
		}
	}
}
