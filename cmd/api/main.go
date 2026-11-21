// Command api is material-flow-control: the relationship-1 REST interface, the two subsystem fact consumers, the
// subsystem command dispatcher, the leg-timeout tick and the outbox relay. Real Postgres, Kafka and subsystems
// only; a missing variable is a boot error naming it.
package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
)

func main() { os.Exit(boot(os.Stdout, os.Getenv)) }

// boot runs the process and returns its exit code. ruling P4-R13: the JSON logger (default level)
// exists before the configuration is read, so a boot error reaches the fleet log pipeline as one JSON
// line naming every invalid or missing variable, and the exit code is 1.
func boot(w io.Writer, getenv func(string) string) int {
	slog.SetDefault(newLoggerTo(w, "info"))
	if err := runMain(w, getenv); err != nil {
		slog.Error("material-flow-control exited with error", "error", err)
		return 1
	}
	return 0
}

func runMain(w io.Writer, getenv func(string) string) error {
	cfg, err := loadConfig(getenv)
	if err != nil {
		return err
	}
	log := newLoggerTo(w, cfg.LogLevel)
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
