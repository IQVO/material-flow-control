package http

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/riandyrn/otelchi"
	otelchimetric "github.com/riandyrn/otelchi/metric"

	"github.com/IQVO/material-flow-control/internal/adapters/inbound/requestlog"
	"github.com/IQVO/material-flow-control/internal/application/usecases"
)

// DefaultServiceName is the otelchi server name when Server.ServiceName is empty.
const DefaultServiceName = "material-flow-control"

// Server is the relationship-1 Open Host Service (ADR 0005).
type Server struct {
	Service     *usecases.Service
	Idempotency func(http.Handler) http.Handler
	Readiness   *Readiness
	ServiceName string
}

// NewRouter builds the chi router: Tier 1 telemetry, the request log, panic recovery, then the routes; every POST
// runs inside the Idempotency middleware's unit of work.
func NewRouter(s *Server, logger *slog.Logger) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	name := s.ServiceName
	if name == "" {
		name = DefaultServiceName
	}
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(otelchi.Middleware(name, otelchi.WithChiRoutes(r)))
	r.Use(otelchimetric.NewServerRequestDuration(otelchimetric.NewBaseConfig(name)))
	r.Use(requestlog.RequestLogger(logger))
	r.Use(recoverPanic)
	r.Get("/healthz", handleHealthz)
	r.Get("/readyz", s.handleReadyz)
	r.Get("/transport-requests/{transportRequestId}", s.handleGetTransport)
	r.Get("/container-tracks/{containerId}", s.handleGetTrack)
	r.Get("/stations/{stationId}", s.handleGetStation)
	r.Get("/subsystems", s.handleSubsystems)
	r.Group(func(r chi.Router) {
		if s.Idempotency != nil {
			r.Use(s.Idempotency)
		}
		r.Post("/transport-requests", s.handleRequestTransport)
		r.Post("/transport-requests/{transportRequestId}/cancellation", s.handleCancel)
		r.Post("/container-tracks/{containerId}/clarification-resolution", s.handleResolve)
	})
	return r
}
