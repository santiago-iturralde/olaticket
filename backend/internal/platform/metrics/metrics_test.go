package metrics

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestMiddlewareRecordsRouteTemplates(t *testing.T) {
	gin.SetMode(gin.TestMode)
	m := New()
	r := gin.New()
	r.Use(m.Middleware())
	r.GET("/events/:id", func(c *gin.Context) { c.Status(http.StatusOK) })
	r.GET("/boom", func(c *gin.Context) { c.Status(http.StatusInternalServerError) })
	r.GET("/stream", func(c *gin.Context) {
		c.Header("Content-Type", "text/event-stream")
		c.Status(http.StatusOK)
	})

	for _, path := range []string{"/events/1", "/events/2", "/boom", "/stream", "/no-existe"} {
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
	}

	res := httptest.NewRecorder()
	m.Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body, _ := io.ReadAll(res.Body)
	out := string(body)

	for _, want := range []string{
		`http_requests_total{method="GET",route="/events/:id",status="200"} 2`, // Plantilla, no /events/1 y /events/2
		`http_requests_total{method="GET",route="/boom",status="500"} 1`,
		`http_requests_total{method="GET",route="unmatched",status="404"} 1`,
		`http_request_duration_seconds_count{method="GET",route="/events/:id"} 2`,
		`http_requests_in_flight 0`,
		`go_goroutines`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("falta %q en /metrics", want)
		}
	}
	// El stream SSE se cuenta pero no entra en la latencia.
	if !strings.Contains(out, `route="/stream",status="200"} 1`) || strings.Contains(out, `http_request_duration_seconds_count{method="GET",route="/stream"}`) {
		t.Errorf("el stream SSE no debería medir latencia:\n%s", out)
	}
	if strings.Contains(out, "/events/1") {
		t.Error("las rutas deben usar la plantilla, no la URL real")
	}
}
