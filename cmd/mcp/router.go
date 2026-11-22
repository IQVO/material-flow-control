package main

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/riandyrn/otelchi"
	otelchimetric "github.com/riandyrn/otelchi/metric"

	"github.com/IQVO/material-flow-control/internal/adapters/inbound/requestlog"
)

// newRouter wraps the MCP handler so the binary is deployable behind Kubernetes probes:
//
//   - GET /healthz -> 200 {"status":"ok"} (open; the probes need no credentials and it leaks nothing).
//   - /mcp and /    -> the MCP Streamable HTTP handler, so warehouse-ops-agent's *_MCP_ENDPOINT
//     convention (".../mcp") and the Kong route (rewritten to /mcp) both work.
//
// Middleware order is the fleet observability standard (ADR 0007), identical to the REST router:
// RequestID, otelchi, the request-duration histogram, the access log, Recoverer. Telemetry must be
// set up before this call. No auth middleware is ever added here.
func newRouter(mcpHandler http.Handler, logger *slog.Logger, serviceName string) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(otelchi.Middleware(serviceName, otelchi.WithChiRoutes(r)))
	r.Use(otelchimetric.NewServerRequestDuration(otelchimetric.NewBaseConfig(serviceName)))
	r.Use(requestlog.RequestLogger(logger))
	r.Use(middleware.Recoverer)
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	r.Mount("/mcp", mcpHandler)
	r.Mount("/", mcpHandler)
	return r
}
