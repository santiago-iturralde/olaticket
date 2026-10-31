package http

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"

	"stock-api/internal/domain"
	"stock-api/internal/platform/auth"
)

// fakeTicket es una entrada del fakeEventStore: a qué evento pertenece y el estado de su orden.
type fakeTicket struct {
	eventID     int64
	orderStatus string
	ticket      domain.Ticket
}

// ScanTicket imita las reglas de mysql.Repository.ScanTicket.
func (s *fakeEventStore) ScanTicket(_ context.Context, ticketID, eventID int64) (*domain.ScanResult, error) {
	t, ok := s.tickets[ticketID]
	if !ok {
		return nil, domain.ErrTicketNotFound
	}
	res := &domain.ScanResult{Ticket: t.ticket, EventID: t.eventID, OrderStatus: t.orderStatus}
	switch {
	case t.eventID != eventID:
		return nil, domain.ErrTicketOtherEvent
	case t.orderStatus != domain.OrderConfirmed:
		return res, domain.ErrTicketNotPaid
	case t.ticket.Ingresado:
		return res, domain.ErrTicketAlreadyUsed
	}
	t.ticket.Ingresado = true
	res.Ingresado = true
	return res, nil
}

func scanCode(t *testing.T, ticketID int64) string {
	t.Helper()
	code, err := auth.GenerateEntryCode(ticketID)
	if err != nil {
		t.Fatal(err)
	}
	return code
}

func TestAdminScanTicket(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret-with-at-least-32-characters")
	store := newFakeEventStore()
	store.tickets = map[int64]*fakeTicket{
		1: {eventID: 1, orderStatus: domain.OrderConfirmed, ticket: domain.Ticket{ID: 1, Nombre: "Ana Pérez", DNI: "30111222"}},
		2: {eventID: 1, orderStatus: domain.OrderReserved, ticket: domain.Ticket{ID: 2, Nombre: "Beto"}},
		3: {eventID: 2, orderStatus: domain.OrderConfirmed, ticket: domain.Ticket{ID: 3, Nombre: "Otro evento"}},
	}
	r := adminRouter(store, domain.RolOrganizador, 1)
	r.POST("/api/admin/eventos/:id/scan", NewAdminHandler(store, store, &fakeUploader{}).ScanTicket)

	scan := func(eventID, code string) (int, map[string]any) {
		res := doJSON(r, http.MethodPost, "/api/admin/eventos/"+eventID+"/scan", `{"codigo":"`+code+`"}`)
		var body map[string]any
		_ = json.Unmarshal(res.Body.Bytes(), &body)
		return res.Code, body
	}

	// Primera lectura: adelante, con los datos del titular para mirar el DNI.
	code, body := scan("1", scanCode(t, 1))
	if code != http.StatusOK || body["resultado"] != "permitido" || body["ticket"].(map[string]any)["dni"] != "30111222" {
		t.Fatalf("primera lectura: %d %v", code, body)
	}
	// Segunda lectura del mismo QR: ya ingresó.
	if code, body := scan("1", scanCode(t, 1)); code != http.StatusConflict || body["resultado"] != "ya_ingreso" {
		t.Fatalf("segunda lectura: %d %v", code, body)
	}

	for name, tc := range map[string]struct {
		event, code string
		want        int
		resultado   string
	}{
		"orden sin pagar":    {"1", scanCode(t, 2), http.StatusForbidden, "no_pagada"},
		"de otro evento":     {"1", scanCode(t, 3), http.StatusNotFound, "invalido"},
		"no existe":          {"1", scanCode(t, 99), http.StatusNotFound, "invalido"},
		"QR adulterado":      {"1", scanCode(t, 1) + "x", http.StatusNotFound, "invalido"},
		"ticket_id pelado":   {"1", "1", http.StatusNotFound, "invalido"},
		"evento de otra org": {"2", scanCode(t, 3), http.StatusNotFound, ""},
	} {
		code, body := scan(tc.event, tc.code)
		if code != tc.want || (tc.resultado != "" && body["resultado"] != tc.resultado) {
			t.Errorf("%s: %d %v, want %d %s", name, code, body, tc.want, tc.resultado)
		}
		// Una entrada de otro evento no revela de quién es.
		if name == "de otro evento" && body["ticket"] != nil {
			t.Errorf("%s: expuso los datos de la entrada", name)
		}
	}
	if store.tickets[3].ticket.Ingresado {
		t.Fatal("se marcó como ingresada una entrada de otro evento")
	}
}

func TestValidateAttendees(t *testing.T) {
	order := &domain.Order{Items: []domain.OrderItem{{SectorID: 10, Quantity: 2}, {SectorID: 11, Quantity: 1}}}
	ok := []asistenteRequest{
		{SectorID: 10, Nombre: "  Ana   Pérez ", DNI: "30.111.222"},
		{SectorID: 11, Nombre: "Beto Gómez", DNI: "30111333"},
		{SectorID: 10, Nombre: "Carla Ruiz", DNI: "aa123456"},
	}
	got, ferr := validateAttendees(order, ok)
	if ferr != nil {
		t.Fatalf("válidos: %+v", ferr)
	}
	if got[0] != (domain.Asistente{SectorID: 10, Nombre: "Ana Pérez", DNI: "30111222"}) || got[2].DNI != "AA123456" {
		t.Fatalf("no normalizó: %+v", got)
	}

	with := func(i int, f func(*asistenteRequest)) []asistenteRequest {
		reqs := append([]asistenteRequest(nil), ok...)
		f(&reqs[i])
		return reqs
	}
	for name, tc := range map[string]struct {
		reqs  []asistenteRequest
		field string
	}{
		"faltan asistentes": {ok[:2], "asistentes"},
		"sector equivocado": {with(1, func(a *asistenteRequest) { a.SectorID = 10 }), "asistentes"},
		"sector ajeno":      {with(1, func(a *asistenteRequest) { a.SectorID = 99 }), "asistentes"},
		"nombre vacío":      {with(1, func(a *asistenteRequest) { a.Nombre = "  " }), "asistentes.1.nombre"},
		"nombre sin letras": {with(1, func(a *asistenteRequest) { a.Nombre = "12345" }), "asistentes.1.nombre"},
		"DNI corto":         {with(2, func(a *asistenteRequest) { a.DNI = "123" }), "asistentes.2.dni"},
		"DNI con símbolos":  {with(2, func(a *asistenteRequest) { a.DNI = "30111222!" }), "asistentes.2.dni"},
		"DNI repetido":      {with(2, func(a *asistenteRequest) { a.DNI = "30111222" }), "asistentes.2.dni"},
	} {
		if _, ferr := validateAttendees(order, tc.reqs); ferr == nil || ferr.Field != tc.field {
			t.Errorf("%s: err = %+v, want field %s", name, ferr, tc.field)
		}
	}
}

// fakeUsers imita la tabla usuarios para la gestión de roles.
type fakeUsers struct {
	roles  map[int64]domain.UserSummary
	orders map[int64]int // órdenes por usuario
	hashes []string      // password_hash recibidos en las altas
}

func (f *fakeUsers) ListUsers(_ context.Context, _ string, scope int64) ([]domain.UserSummary, int, error) {
	var users []domain.UserSummary
	for _, u := range f.roles {
		if f.inScope(u, scope) {
			users = append(users, u)
		}
	}
	return users, len(users), nil
}

// inScope imita el filtro de mysql.ListUsers: el admin ve a todo el equipo, un organizador
// solo a organizadores y porteros de su productora.
func (f *fakeUsers) inScope(u domain.UserSummary, scope int64) bool {
	if scope > 0 {
		return domain.RolDeProductora(u.Rol) && u.OrganizadorID == scope
	}
	return domain.RolDeEquipo(u.Rol)
}

// staff imita lockStaffUser.
func (f *fakeUsers) staff(id, scope int64) (domain.UserSummary, error) {
	u, ok := f.roles[id]
	switch {
	case !ok:
		return u, domain.ErrUserNotFound
	case scope > 0 && !f.inScope(u, scope):
		return u, domain.ErrUserNotFound
	case !domain.RolDeEquipo(u.Rol):
		return u, domain.ErrCustomerAccount
	}
	return u, nil
}

func (f *fakeUsers) UpdateUserRole(_ context.Context, id int64, rol string, organizadorID, scope int64) error {
	u, err := f.staff(id, scope)
	if err != nil {
		return err
	}
	if domain.RolDeProductora(rol) && organizadorID != 1 && organizadorID != 2 {
		return domain.ErrOrganizadorNotFound
	}
	u.Rol, u.OrganizadorID = rol, organizadorID
	f.roles[id] = u
	return nil
}

func (f *fakeUsers) GetUserRole(_ context.Context, id int64) (string, int64, error) {
	u, ok := f.roles[id]
	if !ok {
		return "", 0, domain.ErrUserNotFound
	}
	return u.Rol, u.OrganizadorID, nil
}

// usersRouter arma /api/admin como main.go: rol del JWT (claims) pisado por CurrentRole.
func usersRouter(users *fakeUsers, userID int64, jwtRol string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	h := NewUsersHandler(users)
	r := gin.New()
	admin := r.Group("/api/admin", func(c *gin.Context) {
		c.Set(auth.UserIDContextKey, userID)
		c.Set(auth.RolContextKey, jwtRol)
	}, CurrentRole(users), auth.RequireRole(domain.RolOrganizador, domain.RolAdmin))
	admin.GET("/usuarios", auth.RequireRole(domain.RolAdmin), h.ListUsers)
	admin.PUT("/usuarios/:id/rol", auth.RequireRole(domain.RolAdmin), h.UpdateRole)
	return r
}

func TestUsersRoleManagement(t *testing.T) {
	users := &fakeUsers{roles: map[int64]domain.UserSummary{
		1: {ID: 1, Rol: domain.RolAdmin},
		2: {ID: 2, Rol: domain.RolAdmin},
		3: {ID: 3, Rol: domain.RolOrganizador, OrganizadorID: 1},
		4: {ID: 4, Rol: domain.RolUsuario}, // Cliente
	}}
	admin := usersRouter(users, 1, domain.RolAdmin)

	res := doJSON(admin, http.MethodGet, "/api/admin/usuarios", "")
	var listed struct {
		Usuarios []domain.UserSummary `json:"usuarios"`
	}
	_ = json.Unmarshal(res.Body.Bytes(), &listed)
	if res.Code != http.StatusOK || len(listed.Usuarios) != 3 {
		t.Fatalf("listar: %d %s", res.Code, res.Body)
	}
	for _, u := range listed.Usuarios {
		if u.Rol == domain.RolUsuario {
			t.Fatalf("el listado expone a un cliente: %+v", u)
		}
	}
	for name, tc := range map[string]struct {
		path, body string
		want       int
		field      string
	}{
		"organizador sin productora": {"/api/admin/usuarios/2/rol", `{"rol":"organizador"}`, http.StatusBadRequest, "organizador_id"},
		"productora inexistente":     {"/api/admin/usuarios/2/rol", `{"rol":"organizador","organizador_id":9}`, http.StatusBadRequest, "organizador_id"},
		"rol inválido":               {"/api/admin/usuarios/2/rol", `{"rol":"root"}`, http.StatusBadRequest, "rol"},
		"degradar a cliente":         {"/api/admin/usuarios/2/rol", `{"rol":"usuario"}`, http.StatusBadRequest, "rol"},
		"propio rol":                 {"/api/admin/usuarios/1/rol", `{"rol":"organizador","organizador_id":1}`, http.StatusBadRequest, ""},
		"no existe":                  {"/api/admin/usuarios/99/rol", `{"rol":"admin"}`, http.StatusNotFound, ""},
		"promover a un cliente":      {"/api/admin/usuarios/4/rol", `{"rol":"admin"}`, http.StatusForbidden, ""},
	} {
		res := doJSON(admin, http.MethodPut, tc.path, tc.body)
		if res.Code != tc.want || (tc.field != "" && errorField(t, res) != tc.field) {
			t.Errorf("%s: %d %s", name, res.Code, res.Body)
		}
	}
	if users.roles[1].Rol != domain.RolAdmin || users.roles[2].Rol != domain.RolAdmin || users.roles[4].Rol != domain.RolUsuario {
		t.Fatal("un cambio rechazado modificó roles")
	}

	if res := doJSON(admin, http.MethodPut, "/api/admin/usuarios/2/rol", `{"rol":"organizador","organizador_id":1}`); res.Code != http.StatusOK {
		t.Fatalf("admin -> organizador: %d %s", res.Code, res.Body)
	}
	if u := users.roles[2]; u.Rol != domain.RolOrganizador || u.OrganizadorID != 1 {
		t.Fatalf("usuario 2 = %+v", u)
	}

	// Un organizador no puede gestionar usuarios.
	if res := doJSON(usersRouter(users, 3, domain.RolOrganizador), http.MethodGet, "/api/admin/usuarios", ""); res.Code != http.StatusForbidden {
		t.Fatalf("organizador listando usuarios: %d", res.Code)
	}
}

func TestCurrentRoleOverridesJWT(t *testing.T) {
	users := &fakeUsers{roles: map[int64]domain.UserSummary{
		1: {ID: 1, Rol: domain.RolAdmin},
		5: {ID: 5, Rol: domain.RolUsuario}, // Era admin cuando se emitió su JWT
	}}
	// El JWT todavía dice admin, pero en la base ya lo degradaron: sin acceso.
	if res := doJSON(usersRouter(users, 5, domain.RolAdmin), http.MethodGet, "/api/admin/usuarios", ""); res.Code != http.StatusForbidden {
		t.Fatalf("admin degradado: %d", res.Code)
	}
	// Usuario borrado con un JWT vigente: 401.
	if res := doJSON(usersRouter(users, 77, domain.RolAdmin), http.MethodGet, "/api/admin/usuarios", ""); res.Code != http.StatusUnauthorized {
		t.Fatalf("usuario inexistente: %d", res.Code)
	}
}

func (f *fakeUsers) GetUserSummary(_ context.Context, id int64) (*domain.UserSummary, error) {
	u, ok := f.roles[id]
	if !ok {
		return nil, domain.ErrUserNotFound
	}
	return &u, nil
}

func (f *fakeUsers) emailTaken(email string, except int64) bool {
	for id, u := range f.roles {
		if id != except && u.Email == email {
			return true
		}
	}
	return false
}

func (f *fakeUsers) CreateUser(_ context.Context, u domain.NewUser) (int64, error) {
	if f.emailTaken(u.Email, 0) {
		return 0, domain.ErrEmailTaken
	}
	f.hashes = append(f.hashes, u.PasswordHash)
	id := int64(len(f.roles) + 100)
	f.roles[id] = domain.UserSummary{ID: id, Email: u.Email, Nombre: u.Nombre, Apellido: u.Apellido, Rol: u.Rol, OrganizadorID: u.OrganizadorID}
	return id, nil
}

func (f *fakeUsers) UpdateUserProfile(_ context.Context, id, scope int64, nombre, apellido, email string) error {
	u, err := f.staff(id, scope)
	if err != nil {
		return err
	}
	if f.emailTaken(email, id) {
		return domain.ErrEmailTaken
	}
	u.Nombre, u.Apellido, u.Email = nombre, apellido, email
	f.roles[id] = u
	return nil
}

func (f *fakeUsers) DeleteUser(_ context.Context, id, scope int64) error {
	if _, err := f.staff(id, scope); err != nil {
		return err
	}
	if n := f.orders[id]; n > 0 {
		return &domain.UserHasOrdersError{Orders: n}
	}
	delete(f.roles, id)
	return nil
}

func crudRouter(users *fakeUsers) *gin.Engine {
	r := usersRouter(users, 1, domain.RolAdmin)
	h := NewUsersHandler(users)
	admin := r.Group("/api/admin", func(c *gin.Context) {
		c.Set(auth.UserIDContextKey, int64(1))
	}, CurrentRole(users), auth.RequireRole(domain.RolAdmin))
	admin.POST("/usuarios", h.CreateUser)
	admin.PUT("/usuarios/:id", h.UpdateUser)
	admin.DELETE("/usuarios/:id", h.DeleteUser)
	return r
}

func TestUsersCreate(t *testing.T) {
	users := &fakeUsers{roles: map[int64]domain.UserSummary{
		1: {ID: 1, Email: "admin@x.com", Rol: domain.RolAdmin},
	}}
	r := crudRouter(users)

	res := doJSON(r, http.MethodPost, "/api/admin/usuarios",
		`{"nombre":"  Ana   María ","apellido":"Pérez","email":" Ana@Mail.COM ","password":"Segura123","rol":"organizador","organizador_id":1}`)
	if res.Code != http.StatusCreated {
		t.Fatalf("alta: %d %s", res.Code, res.Body)
	}
	var created domain.UserSummary
	_ = json.Unmarshal(res.Body.Bytes(), &created)
	if created.Email != "ana@mail.com" || created.Nombre != "Ana María" || created.Rol != domain.RolOrganizador {
		t.Fatalf("usuario creado = %+v", created)
	}
	// Se guarda el hash, nunca la contraseña.
	if len(users.hashes) != 1 || users.hashes[0] == "Segura123" || bcrypt.CompareHashAndPassword([]byte(users.hashes[0]), []byte("Segura123")) != nil {
		t.Fatalf("hash inválido: %v", users.hashes)
	}

	for name, tc := range map[string]struct {
		body  string
		want  int
		field string
	}{
		"email repetido":     {`{"nombre":"Otra","email":"ana@mail.com","password":"Segura123","rol":"admin"}`, http.StatusConflict, "email"},
		"email inválido":     {`{"nombre":"Otra","email":"no-es-email","password":"Segura123","rol":"admin"}`, http.StatusBadRequest, "email"},
		"sin nombre":         {`{"nombre":" ","email":"b@mail.com","password":"Segura123","rol":"admin"}`, http.StatusBadRequest, "nombre"},
		"contraseña débil":   {`{"nombre":"Beto","email":"b@mail.com","password":"corta","rol":"admin"}`, http.StatusBadRequest, "password"},
		"crear un cliente":   {`{"nombre":"Beto","email":"b@mail.com","password":"Segura123","rol":"usuario"}`, http.StatusBadRequest, "rol"},
		"rol inválido":       {`{"nombre":"Beto","email":"b@mail.com","password":"Segura123","rol":"root"}`, http.StatusBadRequest, "rol"},
		"org sin productora": {`{"nombre":"Beto","email":"b@mail.com","password":"Segura123","rol":"organizador"}`, http.StatusBadRequest, "organizador_id"},
	} {
		res := doJSON(r, http.MethodPost, "/api/admin/usuarios", tc.body)
		if res.Code != tc.want || errorField(t, res) != tc.field {
			t.Errorf("%s: %d %s", name, res.Code, res.Body)
		}
	}
	if len(users.roles) != 2 {
		t.Fatalf("se crearon usuarios inválidos: %d", len(users.roles))
	}
}

func TestUsersUpdateAndDelete(t *testing.T) {
	users := &fakeUsers{
		roles: map[int64]domain.UserSummary{
			1: {ID: 1, Email: "admin@x.com", Rol: domain.RolAdmin},
			2: {ID: 2, Email: "org-con-compras@x.com", Rol: domain.RolOrganizador, OrganizadorID: 1},
			3: {ID: 3, Email: "nuevo@x.com", Rol: domain.RolOrganizador, OrganizadorID: 1},
			4: {ID: 4, Email: "cliente@x.com", Nombre: "Cliente", Rol: domain.RolUsuario},
		},
		orders: map[int64]int{2: 3},
	}
	r := crudRouter(users)

	// Cuentas de clientes: ni se editan ni se borran desde el panel.
	for _, tc := range []struct{ method, body string }{
		{http.MethodPut, `{"nombre":"Otro","email":"otro@x.com"}`},
		{http.MethodDelete, ""},
	} {
		res := doJSON(r, tc.method, "/api/admin/usuarios/4", tc.body)
		if res.Code != http.StatusForbidden || !strings.Contains(res.Body.String(), "cuentas de clientes") {
			t.Fatalf("%s cliente: %d %s", tc.method, res.Code, res.Body)
		}
	}
	if u, ok := users.roles[4]; !ok || u.Nombre != "Cliente" || u.Email != "cliente@x.com" {
		t.Fatalf("se modificó la cuenta del cliente: %+v", u)
	}

	if res := doJSON(r, http.MethodPut, "/api/admin/usuarios/3", `{"nombre":"Nico","apellido":"Paz","email":"NICO@x.com"}`); res.Code != http.StatusOK {
		t.Fatalf("editar: %d %s", res.Code, res.Body)
	}
	if u := users.roles[3]; u.Nombre != "Nico" || u.Email != "nico@x.com" {
		t.Fatalf("usuario 3 = %+v", u)
	}
	if res := doJSON(r, http.MethodPut, "/api/admin/usuarios/3", `{"nombre":"Nico","email":"admin@x.com"}`); res.Code != http.StatusConflict || errorField(t, res) != "email" {
		t.Fatalf("email de otro: %d %s", res.Code, res.Body)
	}
	if res := doJSON(r, http.MethodPut, "/api/admin/usuarios/99", `{"nombre":"Nico","email":"z@x.com"}`); res.Code != http.StatusNotFound {
		t.Fatalf("editar inexistente: %d", res.Code)
	}

	res := doJSON(r, http.MethodDelete, "/api/admin/usuarios/2", "")
	if res.Code != http.StatusConflict || !strings.Contains(res.Body.String(), "3 compras asociadas") {
		t.Fatalf("borrar con compras: %d %s", res.Code, res.Body)
	}
	if res := doJSON(r, http.MethodDelete, "/api/admin/usuarios/1", ""); res.Code != http.StatusBadRequest {
		t.Fatalf("borrarse a sí mismo: %d", res.Code)
	}
	if res := doJSON(r, http.MethodDelete, "/api/admin/usuarios/3", ""); res.Code != http.StatusNoContent {
		t.Fatalf("borrar sin compras: %d %s", res.Code, res.Body)
	}
	if _, ok := users.roles[3]; ok {
		t.Fatal("no se borró")
	}
	if _, ok := users.roles[2]; !ok {
		t.Fatal("se borró un usuario con compras")
	}
	if res := doJSON(r, http.MethodDelete, "/api/admin/usuarios/3", ""); res.Code != http.StatusNotFound {
		t.Fatalf("borrar dos veces: %d", res.Code)
	}
}

// orgRouter arma /api/admin/usuarios para un organizador de la productora 1 (usuario 10).
func orgRouter(users *fakeUsers) *gin.Engine {
	gin.SetMode(gin.TestMode)
	h := NewUsersHandler(users)
	r := gin.New()
	g := r.Group("/api/admin", func(c *gin.Context) {
		c.Set(auth.UserIDContextKey, int64(10))
	}, CurrentRole(users), auth.RequireRole(domain.RolOrganizador, domain.RolAdmin))
	g.GET("/usuarios", h.ListUsers)
	g.POST("/usuarios", h.CreateUser)
	g.PUT("/usuarios/:id", h.UpdateUser)
	g.PUT("/usuarios/:id/rol", h.UpdateRole)
	g.DELETE("/usuarios/:id", h.DeleteUser)
	return r
}

func TestUsersScopedToProductora(t *testing.T) {
	users := &fakeUsers{roles: map[int64]domain.UserSummary{
		1:  {ID: 1, Email: "admin@x.com", Rol: domain.RolAdmin},
		10: {ID: 10, Email: "org1@x.com", Rol: domain.RolOrganizador, OrganizadorID: 1}, // El que opera
		11: {ID: 11, Email: "portero1@x.com", Rol: domain.RolPortero, OrganizadorID: 1},
		20: {ID: 20, Email: "org2@x.com", Rol: domain.RolOrganizador, OrganizadorID: 2},
		30: {ID: 30, Email: "cliente@x.com", Rol: domain.RolUsuario},
	}}
	r := orgRouter(users)

	res := doJSON(r, http.MethodGet, "/api/admin/usuarios", "")
	var listed struct {
		Usuarios []domain.UserSummary `json:"usuarios"`
	}
	_ = json.Unmarshal(res.Body.Bytes(), &listed)
	ids := map[int64]bool{}
	for _, u := range listed.Usuarios {
		ids[u.ID] = true
	}
	if res.Code != http.StatusOK || len(ids) != 2 || !ids[10] || !ids[11] {
		t.Fatalf("el organizador ve: %v (%d)", ids, res.Code)
	}

	// Alta de un portero: la productora se completa sola con la del organizador.
	res = doJSON(r, http.MethodPost, "/api/admin/usuarios", `{"nombre":"Puerta","email":"puerta@x.com","password":"Segura123","rol":"portero"}`)
	if res.Code != http.StatusCreated {
		t.Fatalf("alta portero: %d %s", res.Code, res.Body)
	}
	var created domain.UserSummary
	_ = json.Unmarshal(res.Body.Bytes(), &created)
	if created.Rol != domain.RolPortero || created.OrganizadorID != 1 {
		t.Fatalf("portero creado = %+v", created)
	}

	for name, tc := range map[string]struct {
		method, path, body string
		want               int
	}{
		"crear un admin":            {http.MethodPost, "/api/admin/usuarios", `{"nombre":"X","email":"x1@x.com","password":"Segura123","rol":"admin"}`, http.StatusForbidden},
		"crear en otra productora":  {http.MethodPost, "/api/admin/usuarios", `{"nombre":"X","email":"x2@x.com","password":"Segura123","rol":"portero","organizador_id":2}`, http.StatusForbidden},
		"ascender a admin":          {http.MethodPut, "/api/admin/usuarios/11/rol", `{"rol":"admin"}`, http.StatusForbidden},
		"mover a otra productora":   {http.MethodPut, "/api/admin/usuarios/11/rol", `{"rol":"portero","organizador_id":2}`, http.StatusForbidden},
		"editar de otra productora": {http.MethodPut, "/api/admin/usuarios/20", `{"nombre":"X","email":"x3@x.com"}`, http.StatusNotFound},
		"editar a un admin":         {http.MethodPut, "/api/admin/usuarios/1", `{"nombre":"X","email":"x4@x.com"}`, http.StatusNotFound},
		"editar a un cliente":       {http.MethodPut, "/api/admin/usuarios/30", `{"nombre":"X","email":"x5@x.com"}`, http.StatusNotFound},
		"rol de otra productora":    {http.MethodPut, "/api/admin/usuarios/20/rol", `{"rol":"portero"}`, http.StatusNotFound},
		"borrar de otra productora": {http.MethodDelete, "/api/admin/usuarios/20", "", http.StatusNotFound},
		"borrar a un admin":         {http.MethodDelete, "/api/admin/usuarios/1", "", http.StatusNotFound},
	} {
		if res := doJSON(r, tc.method, tc.path, tc.body); res.Code != tc.want {
			t.Errorf("%s: %d %s, want %d", name, res.Code, res.Body, tc.want)
		}
	}
	if users.roles[20].Email != "org2@x.com" || users.roles[1].Email != "admin@x.com" || users.roles[11].Rol != domain.RolPortero {
		t.Fatal("un cambio rechazado modificó usuarios")
	}

	// Dentro de su productora sí: portero -> organizador, editar y borrar.
	if res := doJSON(r, http.MethodPut, "/api/admin/usuarios/11/rol", `{"rol":"organizador"}`); res.Code != http.StatusOK {
		t.Fatalf("portero -> organizador: %d %s", res.Code, res.Body)
	}
	if u := users.roles[11]; u.Rol != domain.RolOrganizador || u.OrganizadorID != 1 {
		t.Fatalf("usuario 11 = %+v", u)
	}
	if res := doJSON(r, http.MethodPut, "/api/admin/usuarios/11", `{"nombre":"Nuevo","email":"p1@x.com"}`); res.Code != http.StatusOK {
		t.Fatalf("editar propio equipo: %d %s", res.Code, res.Body)
	}
	if res := doJSON(r, http.MethodDelete, "/api/admin/usuarios/11", ""); res.Code != http.StatusNoContent {
		t.Fatalf("borrar propio equipo: %d %s", res.Code, res.Body)
	}
}

func TestAdminCreatesPortero(t *testing.T) {
	users := &fakeUsers{roles: map[int64]domain.UserSummary{1: {ID: 1, Email: "admin@x.com", Rol: domain.RolAdmin}}}
	r := crudRouter(users)
	if res := doJSON(r, http.MethodPost, "/api/admin/usuarios", `{"nombre":"P","email":"p@x.com","password":"Segura123","rol":"portero"}`); res.Code != http.StatusBadRequest || errorField(t, res) != "organizador_id" {
		t.Fatalf("portero sin productora: %d %s", res.Code, res.Body)
	}
	if res := doJSON(r, http.MethodPost, "/api/admin/usuarios", `{"nombre":"P","email":"p@x.com","password":"Segura123","rol":"portero","organizador_id":2}`); res.Code != http.StatusCreated {
		t.Fatalf("portero: %d %s", res.Code, res.Body)
	}
}

func TestPorteroScansOnlyOwnProductora(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret-with-at-least-32-characters")
	store := newFakeEventStore()
	store.tickets = map[int64]*fakeTicket{
		1: {eventID: 1, orderStatus: domain.OrderConfirmed, ticket: domain.Ticket{ID: 1, Nombre: "Ana"}},
	}
	h := NewAdminHandler(store, store, &fakeUploader{})
	code := `{"codigo":"` + scanCode(t, 1) + `"}`

	for name, tc := range map[string]struct {
		organizadorID int64
		want          int
	}{
		"portero de otra productora": {2, http.StatusNotFound}, // El evento 1 es de la productora 1
		"portero sin productora":     {0, http.StatusForbidden},
		"portero de la productora":   {1, http.StatusOK},
	} {
		gin.SetMode(gin.TestMode)
		r := gin.New()
		r.Use(func(c *gin.Context) {
			c.Set(auth.RolContextKey, domain.RolPortero)
			c.Set(auth.OrganizadorIDContextKey, tc.organizadorID)
		})
		r.POST("/api/admin/eventos/:id/scan", h.ScanTicket)
		r.GET("/api/admin/escaner/eventos", h.ScannerEvents)
		if res := doJSON(r, http.MethodPost, "/api/admin/eventos/1/scan", code); res.Code != tc.want {
			t.Errorf("%s: %d %s, want %d", name, res.Code, res.Body, tc.want)
		}
		if tc.organizadorID == 2 {
			res := doJSON(r, http.MethodGet, "/api/admin/escaner/eventos", "")
			if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "Evento B") || strings.Contains(res.Body.String(), "Evento A") ||
				strings.Contains(res.Body.String(), "recaudacion") {
				t.Errorf("eventos del portero de la productora 2: %d %s", res.Code, res.Body)
			}
		}
	}
}
