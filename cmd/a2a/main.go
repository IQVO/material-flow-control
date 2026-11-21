// Command a2a is material-flow-control's read-only A2A agent (ADR 0007): it wires the layout file and the
// Postgres read repositories into the usecases.Reader (the SAME queries the REST and MCP adapters serve),
// hands it to the inbound A2A adapter and serves A2A JSON-RPC 2.0 over HTTP, with the Agent Card at
// /.well-known/agent-card.json. It is a third, independent deployable beside cmd/api and cmd/mcp, composed
// exactly like cmd/mcp.
//
// READ-ONLY: no unit of work, flow lock, outbox, command outbox, inbox, subsystem client or Kafka client is
// wired here, and no outbound A2A client exists, so this binary has no seam to write through, never dials
// Kafka, never calls a subsystem and never starts the relay or the dispatcher. There is NO auth of any kind
// (fleet decision 2026-09-11) and the card declares no security scheme: the ClusterIP boundary is the control.
//
// Environment:
//
//	A2A_ADDR                     listen address (default :8093)
//	A2A_AGENT_URL                REQUIRED. absolute http(s) URL the Agent Card advertises, e.g.
//	                             http://material-flow-control-a2a.warehouse-systems.svc.cluster.local:8093/
//	DATABASE_URL                 REQUIRED. the Postgres DSN cmd/api owns
//	MIGRATIONS_DATABASE_URL      direct DSN for the boot migrations (default DATABASE_URL)
//	MFC_LAYOUT_FILE              REQUIRED. the material-flow layout YAML (ADR 0002), the same file cmd/api reads
//	LOG_LEVEL                    debug | info | warn | error (default info)
//	OTEL_SERVICE_NAME            service.name (default material-flow-control-a2a)
//	OTEL_EXPORTER_OTLP_ENDPOINT  Collector OTLP/gRPC endpoint; empty = telemetry off (ADR 0007)
//	SERVICE_VERSION              service.version and the Agent Card version (default dev)
//	ENVIRONMENT                  deployment.environment.name (read by the telemetry adapter)
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	inbounda2a "github.com/IQVO/material-flow-control/internal/adapters/inbound/a2a"
	"github.com/IQVO/material-flow-control/internal/adapters/outbound/postgres"
	"github.com/IQVO/material-flow-control/internal/adapters/outbound/system"
	"github.com/IQVO/material-flow-control/internal/application/usecases"
	layoutconfig "github.com/IQVO/material-flow-control/internal/config"
)

// shutdownTimeout bounds the HTTP drain.
const shutdownTimeout = 10 * time.Second

type config struct {
	Addr, AgentURL, DatabaseURL, MigrationsURL, LayoutFile, LogLevel string
	ServiceName, ServiceVersion, OTLPEndpoint                        string
}

func loadConfig(getenv func(string) string) (config, error) {
	or := func(k, def string) string {
		if v := getenv(k); v != "" {
			return v
		}
		return def
	}
	c := config{Addr: or("A2A_ADDR", ":8093"), AgentURL: strings.TrimSpace(getenv("A2A_AGENT_URL")), DatabaseURL: getenv("DATABASE_URL"),
		MigrationsURL: postgres.MigrationsURL(getenv), LayoutFile: getenv("MFC_LAYOUT_FILE"), LogLevel: or("LOG_LEVEL", "info"),
		ServiceName: or("OTEL_SERVICE_NAME", "material-flow-control-a2a"), ServiceVersion: getenv("SERVICE_VERSION"),
		OTLPEndpoint: getenv("OTEL_EXPORTER_OTLP_ENDPOINT")}
	var problems []error
	if u, err := url.Parse(c.AgentURL); err != nil || c.AgentURL == "" || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		problems = append(problems, errors.New("A2A_AGENT_URL is required: an absolute http(s) URL the Agent Card advertises "+
			"(e.g. http://material-flow-control-a2a.warehouse-systems.svc.cluster.local:8093/)"))
	}
	if c.DatabaseURL == "" {
		problems = append(problems, errors.New("DATABASE_URL is required: the A2A agent reads material-flow-control's Postgres"))
	}
	if c.LayoutFile == "" {
		problems = append(problems, errors.New("MFC_LAYOUT_FILE is required: the A2A agent reads the same layout as cmd/api"))
	}
	return c, errors.Join(problems...)
}

func main() { os.Exit(boot(os.Stdout, os.Getenv)) }

// boot runs the process and returns its exit code. ruling P4-R13 (cmd/api and cmd/mcp parity): the JSON logger
// (default level) exists before the configuration is read, so a boot error reaches the fleet log pipeline as
// one JSON line naming every missing variable, and the exit code is 1.
func boot(w io.Writer, getenv func(string) string) int {
	slog.SetDefault(newLogger(w, "info"))
	if err := run(w, getenv); err != nil {
		slog.Error("material-flow-control a2a exited with error", "error", err)
		return 1
	}
	return 0
}

func run(w io.Writer, getenv func(string) string) error {
	cfg, err := loadConfig(getenv)
	if err != nil {
		return err
	}
	log := newLogger(w, cfg.LogLevel)
	slog.SetDefault(log)
	flush, err := startTelemetry(log, cfg.ServiceName, cfg.ServiceVersion, cfg.OTLPEndpoint)
	if err != nil {
		return err
	}
	defer flush()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	reads, closeReads, err := buildReads(ctx, cfg)
	if err != nil {
		return err
	}
	defer closeReads()
	ln, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: newAgent(inbounda2a.Deps{Reads: reads}, cfg.AgentURL, cardVersion(cfg.ServiceVersion), log, cfg.ServiceName),
		ReadHeaderTimeout: 5 * time.Second}
	log.Info("material-flow-control a2a listening (JSON-RPC)", "addr", ln.Addr().String(), "agent_url", cfg.AgentURL)
	return serve(ctx, srv, ln)
}

// buildReads is cmd/mcp's: it loads the layout, runs the idempotent migrations (this binary may boot before
// cmd/api) and wires the Reader over the Postgres read repositories. READ-ONLY wiring: no unit of work, lock,
// outbox, command outbox, inbox, subsystem client or Kafka client is constructed.
func buildReads(ctx context.Context, cfg config) (*usecases.Reader, func(), error) {
	l, err := layoutconfig.LoadLayout(cfg.LayoutFile)
	if err != nil {
		return nil, nil, err
	}
	if err := postgres.RunMigrations(cfg.MigrationsURL); err != nil {
		return nil, nil, fmt.Errorf("migrations: %w", err)
	}
	pool, err := postgres.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, nil, fmt.Errorf("postgres: %w", err)
	}
	r, err := usecases.NewReader(l, usecases.DefaultConfig(), usecases.ReadDeps{Requests: postgres.NewRequests(pool),
		Orders: postgres.NewOrders(pool), Tracks: postgres.NewTracks(pool), Stations: postgres.NewStations(pool),
		Capacities: postgres.NewCapacities(pool), Units: postgres.NewUnits(pool), Clock: system.Clock{}})
	if err != nil {
		pool.Close()
		return nil, nil, err
	}
	return r, pool.Close, nil
}

// cardVersion is the Agent Card version: SERVICE_VERSION, "dev" when unset.
func cardVersion(v string) string {
	if v == "" {
		return "dev"
	}
	return v
}

// serve runs srv on ln until ctx ends, then drains it.
func serve(ctx context.Context, srv *http.Server, ln net.Listener) error {
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()
	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	}
	drain, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	return srv.Shutdown(drain)
}
