package main

import (
	"log/slog"
	"net/http"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/riandyrn/otelchi"
	otelchimetric "github.com/riandyrn/otelchi/metric"

	inbounda2a "github.com/IQVO/material-flow-control/internal/adapters/inbound/a2a"
	"github.com/IQVO/material-flow-control/internal/adapters/inbound/requestlog"
)

// newAgent composes the A2A surface over the read use cases and the card advertising agentURL.
func newAgent(deps inbounda2a.Deps, agentURL, version string, logger *slog.Logger, serviceName string) http.Handler {
	card := inbounda2a.Card(inbounda2a.Config{AgentURL: agentURL, Version: version})
	return newRouter(inbounda2a.Handler(inbounda2a.NewExecutor(deps)), card, logger, serviceName)
}

// newRouter wraps the A2A handler so the binary is deployable behind Kubernetes probes:
//
//   - GET /healthz answers 200 {"status":"ok"} (open; leaks no skill or state).
//   - The Agent Card is served at /.well-known/agent-card.json (the discovery contract).
//   - The JSON-RPC endpoint is mounted at "/" (Kong rewrites /api/a2a/material-flow-control to /) and at
//     "/a2a" (the fleet's <CTX>_A2A_ENDPOINT convention).
//
// Middleware order is the fleet observability standard (ADR 0007). No auth middleware, ever.
func newRouter(a2aHandler http.Handler, card *a2a.AgentCard, logger *slog.Logger, serviceName string) http.Handler {
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
	r.Handle(a2asrv.WellKnownAgentCardPath, a2asrv.NewStaticAgentCardHandler(card))
	r.Mount("/a2a", a2aHandler)
	r.Mount("/", a2aHandler)
	return r
}
