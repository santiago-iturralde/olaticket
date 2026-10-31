// Package metrics expone métricas HTTP de la API para Prometheus.
package metrics

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// unmatchedRoute agrupa los pedidos a rutas inexistentes (scanners, typos): con la URL real
// como etiqueta cada uno crearía una serie nueva en Prometheus.
const unmatchedRoute = "unmatched"

// Metrics son los colectores HTTP de la API. Usa su propio registro (no el global) para que
// los tests puedan crear varios sin chocar.
type Metrics struct {
	registry *prometheus.Registry
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
	inFlight prometheus.Gauge
}

func New() *Metrics {
	m := &Metrics{
		registry: prometheus.NewRegistry(),
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Pedidos HTTP atendidos, por método, ruta (plantilla, ej. /events/:id) y código de estado.",
		}, []string{"method", "route", "status"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "Latencia de los pedidos HTTP (sin los streams SSE, que quedan abiertos minutos).",
			Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
		}, []string{"method", "route"}),
		inFlight: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "http_requests_in_flight",
			Help: "Pedidos HTTP en curso (incluye los streams SSE abiertos).",
		}),
	}
	m.registry.MustRegister(
		m.requests, m.duration, m.inFlight,
		// Memoria, goroutines, GC, CPU y descriptores del proceso.
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	return m
}

// Middleware mide cada pedido. Va antes que el resto de los middlewares para contar
// también lo que ellos cortan (401, 429 del rate limit, 403...).
func (m *Metrics) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		m.inFlight.Inc()
		defer m.inFlight.Dec()

		c.Next()

		route := c.FullPath()
		if route == "" {
			route = unmatchedRoute
		}
		method := c.Request.Method
		m.requests.WithLabelValues(method, route, strconv.Itoa(c.Writer.Status())).Inc()
		if !strings.HasPrefix(c.Writer.Header().Get("Content-Type"), "text/event-stream") {
			m.duration.WithLabelValues(method, route).Observe(time.Since(start).Seconds())
		}
	}
}

// Handler sirve las métricas en el formato de Prometheus.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{Registry: m.registry})
}
