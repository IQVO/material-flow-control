// Command api is material-flow-control: the relationship-1 REST interface, the two subsystem fact consumers, the
// subsystem command dispatcher, the leg-timeout tick and the outbox relay. Real Postgres, Kafka and subsystems
// only; a missing variable is a boot error naming it.
package main

import (
	"context"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	if err := runMain(); err != nil {
		slog.Error("material-flow-control exited with error", "error", err)
		os.Exit(1)
	}
}

func runMain() error {
	cfg, err := loadConfig(os.Getenv)
	if err != nil {
		return err
	}
	log := newLogger(cfg.LogLevel)
	slog.SetDefault(log)
	flush, err := startTelemetry(log, cfg)
	if err != nil {
		return err
	}
	defer flush()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	a, err := wire(ctx, cfg, log)
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return err
	}
	log.InfoContext(ctx, "material-flow-control listening", "addr", cfg.Addr, "site", "layout "+cfg.LayoutFile)
	return a.run(ctx, ln)
}
