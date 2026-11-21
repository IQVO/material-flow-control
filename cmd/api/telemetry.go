package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/IQVO/material-flow-control/internal/adapters/outbound/telemetry"
)

// telemetryFlushTimeout bounds the final export attempt, so an unreachable Collector cannot
// stretch a shutdown past what the orchestrator waits for.
const telemetryFlushTimeout = 5 * time.Second

// startTelemetry brings OpenTelemetry up (ADR 0004) and returns the flush to defer. Export is
// non-blocking: an unreachable Collector costs telemetry, never availability. With no OTLP
// endpoint nothing is installed and the flush does nothing. A failed flush is logged, never
// returned, so a clean shutdown never looks like a crash. It must run before wire builds the
// router: otelchimetric binds the global MeterProvider at construction.
func startTelemetry(log *slog.Logger, cfg config) (func(), error) {
	shutdown, err := telemetry.Setup(context.Background(), cfg.ServiceName, cfg.ServiceVersion, cfg.OTLPEndpoint)
	if err != nil {
		return nil, err
	}
	if cfg.OTLPEndpoint == "" {
		log.Info("telemetry disabled: OTEL_EXPORTER_OTLP_ENDPOINT is not set", "service_name", cfg.ServiceName)
	} else {
		log.Info("telemetry configured", "service_name", cfg.ServiceName, "otlp_endpoint", cfg.OTLPEndpoint)
	}
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), telemetryFlushTimeout)
		defer cancel()
		if err := shutdown(ctx); err != nil {
			log.Warn("telemetry flush failed on shutdown", "error", err)
		}
	}, nil
}

// newLoggerTo is the JSON logger on w at level, stamping trace_id/span_id on every record
// written under a span.
func newLoggerTo(w io.Writer, level string) *slog.Logger {
	lv := slog.LevelInfo
	switch level {
	case "debug":
		lv = slog.LevelDebug
	case "warn", "warning":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	}
	return slog.New(telemetry.WithTraceContext(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: lv})))
}

// newLogger is newLoggerTo on stdout.
func newLogger(level string) *slog.Logger { return newLoggerTo(os.Stdout, level) }
