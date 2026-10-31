package http

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"stock-api/internal/domain"
)

// fakeProductoras imita la tabla organizadores con sus contadores.
type fakeProductoras struct {
	items  map[int64]*domain.ProductoraSummary
	nextID int64
}

func (f *fakeProductoras) ListProductoras(context.Context) ([]domain.ProductoraSummary, error) {
	out := []domain.ProductoraSummary{}
	for _, p := range f.items {
		out = append(out, *p)
	}
	return out, nil
}

func (f *fakeProductoras) GetProductora(_ context.Context, id int64) (*domain.ProductoraSummary, error) {
	p, ok := f.items[id]
	if !ok {
		return nil, domain.ErrOrganizadorNotFound
	}
	return p, nil
}

func (f *fakeProductoras) CreateProductora(_ context.Context, o domain.Organizador) (int64, error) {
	f.nextID++
	o.ID = f.nextID
	f.items[o.ID] = &domain.ProductoraSummary{Organizador: o}
	return o.ID, nil
}

func (f *fakeProductoras) UpdateProductora(_ context.Context, o domain.Organizador) error {
	p, ok := f.items[o.ID]
	if !ok {
		return domain.ErrOrganizadorNotFound
	}
	p.Organizador = o
	return nil
}

func (f *fakeProductoras) DeleteProductora(_ context.Context, id int64) error {
	p, ok := f.items[id]
	if !ok {
		return domain.ErrOrganizadorNotFound
	}
	if p.Eventos > 0 || p.Usuarios > 0 {
		return &domain.ProductoraInUseError{Eventos: p.Eventos, Usuarios: p.Usuarios}
	}
	delete(f.items, id)
	return nil
}

func TestProductorasCRUD(t *testing.T) {
	store := &fakeProductoras{nextID: 10, items: map[int64]*domain.ProductoraSummary{
		1: {Organizador: domain.Organizador{ID: 1, Nombre: "Con eventos"}, Eventos: 3, Usuarios: 1},
	}}
	gin.SetMode(gin.TestMode)
	h := NewProductorasHandler(store)
	r := gin.New()
	r.GET("/api/admin/productoras", h.List)
	r.POST("/api/admin/productoras", h.Create)
	r.PUT("/api/admin/productoras/:id", h.Update)
	r.DELETE("/api/admin/productoras/:id", h.Delete)

	res := doJSON(r, http.MethodPost, "/api/admin/productoras", `{"nombre":"  Teatro   Municipal ","email_contacto":" Info@Teatro.com ","telefono":"+54 2284 123456"}`)
	if res.Code != http.StatusCreated {
		t.Fatalf("alta: %d %s", res.Code, res.Body)
	}
	var created domain.ProductoraSummary
	_ = json.Unmarshal(res.Body.Bytes(), &created)
	if created.Nombre != "Teatro Municipal" || created.EmailContacto != "info@teatro.com" {
		t.Fatalf("productora creada = %+v", created)
	}

	for name, tc := range map[string]struct{ body, field string }{
		"sin nombre":    {`{"nombre":" ","email_contacto":"a@b.com"}`, "nombre"},
		"sin email":     {`{"nombre":"X","email_contacto":""}`, "email_contacto"},
		"email malo":    {`{"nombre":"X","email_contacto":"no-email"}`, "email_contacto"},
		"tel muy largo": {`{"nombre":"X","email_contacto":"a@b.com","telefono":"` + strings.Repeat("1", 31) + `"}`, "telefono"},
	} {
		if res := doJSON(r, http.MethodPost, "/api/admin/productoras", tc.body); res.Code != http.StatusBadRequest || errorField(t, res) != tc.field {
			t.Errorf("%s: %d %s", name, res.Code, res.Body)
		}
	}

	if res := doJSON(r, http.MethodPut, "/api/admin/productoras/11", `{"nombre":"Teatro","email_contacto":"t@x.com"}`); res.Code != http.StatusOK {
		t.Fatalf("editar: %d %s", res.Code, res.Body)
	}
	if res := doJSON(r, http.MethodPut, "/api/admin/productoras/99", `{"nombre":"Teatro","email_contacto":"t@x.com"}`); res.Code != http.StatusNotFound {
		t.Fatalf("editar inexistente: %d", res.Code)
	}

	res = doJSON(r, http.MethodDelete, "/api/admin/productoras/1", "")
	if res.Code != http.StatusConflict || !strings.Contains(res.Body.String(), "3 eventos y 1 usuario") {
		t.Fatalf("borrar con eventos: %d %s", res.Code, res.Body)
	}
	if res := doJSON(r, http.MethodDelete, "/api/admin/productoras/11", ""); res.Code != http.StatusNoContent {
		t.Fatalf("borrar vacía: %d %s", res.Code, res.Body)
	}
	if res := doJSON(r, http.MethodDelete, "/api/admin/productoras/11", ""); res.Code != http.StatusNotFound {
		t.Fatalf("borrar dos veces: %d", res.Code)
	}
}
