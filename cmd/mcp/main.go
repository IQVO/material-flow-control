// Command mcp is material-flow-control's read-only MCP server (ADR 0007): it wires the layout file and the
// Postgres read repositories into the usecases.Reader (the SAME queries the REST adapter serves), hands it to
// the inbound MCP adapter and serves MCP over Streamable HTTP only (no stdio, no SSE), stateless. It is a
// second, independent deployable beside cmd/api.
//
// READ-ONLY: no unit of work, flow lock, outbox, command outbox, inbox, subsystem client or Kafka client is
// wired here, so this binary has no seam to write through, never dials Kafka, never calls a subsystem and
// never starts the relay or the dispatcher. There is NO auth of any kind (fleet decision 2026-09-11):
// access control is the in-cluster ClusterIP boundary.
//
// Environment:
//
//	MCP_ADDR                     listen address (default :8090)
//	DATABASE_URL                 REQUIRED. the Postgres DSN cmd/api owns
//	MIGRATIONS_DATABASE_URL      direct DSN for the boot migrations (default DATABASE_URL)
//	MFC_LAYOUT_FILE              REQUIRED. the material-flow layout YAML (ADR 0002), the same file cmd/api reads
//	LOG_LEVEL                    debug | info | warn | error, any case (default info; another value is a boot error)
//	OTEL_SERVICE_NAME            service.name (default material-flow-control-mcp)
//	OTEL_EXPORTER_OTLP_ENDPOINT  Collector OTLP/gRPC endpoint; empty = telemetry off (ADR 0007)
//	SERVICE_VERSION              service.version (default dev)
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
	"os"
	"os/signal"
	"syscall"
	"time"

	inboundmcp "github.com/IQVO/material-flow-control/internal/adapters/inbound/mcp"
	"github.com/IQVO/material-flow-control/internal/adapters/outbound/postgres"
	"github.com/IQVO/material-flow-control/internal/adapters/outbound/system"
	"github.com/IQVO/material-flow-control/internal/adapters/outbound/telemetry"
	"github.com/IQVO/material-flow-control/internal/application/usecases"
	layoutconfig "github.com/IQVO/material-flow-control/internal/config"
)

// shutdownTimeout bounds the HTTP drain.
const shutdownTimeout = 10 * time.Second

type config struct {
	Addr, DatabaseURL, MigrationsURL, LayoutFile, LogLevel string
	ServiceName, ServiceVersion, OTLPEndpoint              string
}

func loadConfig(getenv func(string) string) (config, error) {
	or := func(k, def string) string {
		if v := getenv(k); v != "" {
			return v
		}
		return def
	}
	c := config{Addr: or("MCP_ADDR", ":8090"), DatabaseURL: getenv("DATABASE_URL"), MigrationsURL: postgres.MigrationsURL(getenv),
		LayoutFile: getenv("MFC_LAYOUT_FILE"), LogLevel: or("LOG_LEVEL", "info"),
		ServiceName: or("OTEL_SERVICE_NAME", "material-flow-control-mcp"), ServiceVersion: getenv("SERVICE_VERSION"),
		OTLPEndpoint: getenv("OTEL_EXPORTER_OTLP_ENDPOINT")}
	var problems []error
	if c.DatabaseURL == "" {
		problems = append(problems, errors.New("DATABASE_URL is required: the MCP server reads material-flow-control's Postgres"))
	}
	if c.LayoutFile == "" {
		problems = append(problems, errors.New("MFC_LAYOUT_FILE is required: the MCP server reads the same layout as cmd/api"))
	}
	// carry (plan 03 Task 16 review parity): a set-but-unknown LOG_LEVEL is a boot error, never a silent info.
	if _, err := telemetry.ParseLogLevel(c.LogLevel); err != nil {
		problems = append(problems, err)
	}
	return c, errors.Join(problems...)
}

func main() { os.Exit(boot(os.Stdout, os.Getenv)) }

// boot runs the process and returns its exit code. ruling P4-R13 (cmd/api parity): the JSON logger
// (default level) exists before the configuration is read, so a boot error reaches the fleet log
// pipeline as one JSON line naming every missing variable, and the exit code is 1.
func boot(w io.Writer, getenv func(string) string) int {
	slog.SetDefault(newLogger(w, "info"))
	if err := run(w, getenv); err != nil {
		slog.Error("material-flow-control mcp exited with error", "error", err)
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
	// carry (Task 20 review): bind first, so a busy address is the boot error before any migration or pool is
	// set up; a failed buildReads closes the listener again.
	ln, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return err
	}
	reads, closeReads, err := buildReads(ctx, cfg)
	if err != nil {
		_ = ln.Close()
		return err
	}
	defer closeReads()
	srv := &http.Server{Handler: newRouter(inboundmcp.Handler(inboundmcp.NewServer(inboundmcp.Deps{Reads: reads})), log, cfg.ServiceName),
		ReadHeaderTimeout: 5 * time.Second}
	log.Info("material-flow-control mcp listening (Streamable HTTP)", "addr", ln.Addr().String())
	return serve(ctx, srv, ln)
}

// buildReads loads the layout, runs the idempotent migrations (this binary may boot before cmd/api) and wires the
// Reader over the Postgres read repositories. READ-ONLY wiring: no unit of work, lock, outbox, command outbox,
// inbox, subsystem client or Kafka client is constructed.
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
