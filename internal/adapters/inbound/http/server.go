// Package http is the REST inbound adapter: every route of apis/openapi.yaml
// on a chi router, RFC 7807 problem documents for every error, and the
// liveness/readiness/metrics endpoints. It has no auth middleware (fleet
// decision 2026-09-11; internal/architecture's TestNoAuthMiddlewareReintroduced).
package http

import (
	"net/http"
	"os"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"
	"github.com/riandyrn/otelchi"
	otelchimetric "github.com/riandyrn/otelchi/metric"

	"github.com/claudioed/product-master/internal/application/usecases"
)

// DefaultServiceName labels this service for telemetry.
const DefaultServiceName = "product-master"

// defaultCORSAllowedOrigins is the local-dev default for
// CORS_ALLOWED_ORIGINS (comma-separated).
const defaultCORSAllowedOrigins = "http://localhost:5173"

// Server holds the use cases the handlers call.
type Server struct {
	RegisterProduct   *usecases.RegisterProduct
	ClassifyProduct   *usecases.ClassifyProduct
	DeclareDimensions *usecases.DeclareDimensions
	RecordMeasurement *usecases.RecordMeasurement
	GetProduct        *usecases.GetProduct
	ListProducts      *usecases.ListProducts

	// Readiness backs GET /readyz; nil means always ready.
	Readiness *Readiness
	// Metrics backs GET /metrics (a Prometheus handler built by cmd/);
	// nil leaves the route unregistered.
	Metrics http.Handler
}

// NewRouter builds the chi router for every endpoint in
// .claude/rules/rest-api.md. otelchi runs first so every handler runs inside
// a span labelled with the route pattern (e.g. "/products/{sku}").
func NewRouter(s *Server) http.Handler {
	r := chi.NewRouter()
	r.Use(otelchi.Middleware(DefaultServiceName, otelchi.WithChiRoutes(r)))
	r.Use(otelchimetric.NewServerRequestDuration(otelchimetric.NewBaseConfig(DefaultServiceName)))
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   corsAllowedOrigins(),
		AllowedMethods:   []string{http.MethodGet, http.MethodPut},
		AllowedHeaders:   []string{"Accept", "Content-Type"},
		AllowCredentials: false,
	}))

	r.Get("/healthz", handleHealthz)
	r.Get("/readyz", s.handleReadyz)
	if s.Metrics != nil {
		r.Method(http.MethodGet, "/metrics", s.Metrics)
	}

	r.Get("/products", s.handleListProducts)
	r.Put("/products/{sku}", s.handleRegisterProduct)
	r.Get("/products/{sku}", s.handleGetProduct)
	r.Put("/products/{sku}/classification", s.handleClassifyProduct)
	r.Get("/products/{sku}/classification", s.handleGetClassification)
	r.Put("/products/{sku}/dimensions/declared", s.handleDeclareDimensions)
	r.Put("/products/{sku}/dimensions/measured", s.handleRecordMeasurement)
	r.Get("/products/{sku}/physical-profile", s.handleGetPhysicalProfile)
	return r
}

func corsAllowedOrigins() []string {
	raw := os.Getenv("CORS_ALLOWED_ORIGINS")
	if raw == "" {
		raw = defaultCORSAllowedOrigins
	}
	parts := strings.Split(raw, ",")
	for i, p := range parts {
		parts[i] = strings.TrimSpace(p)
	}
	return parts
}

func handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, statusBody{Status: "ok"})
}
