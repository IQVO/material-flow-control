package main

import (
	"context"
	"io"
	"log/slog"
	"time"

	"github.com/IQVO/material-flow-control/internal/adapters/outbound/telemetry"
)

// telemetryFlushTimeout bounds the final export attempt, so an unreachable Collector cannot
// stretch a shutdown past what the orchestrator waits for.
const telemetryFlushTimeout = 5 * time.Second

// startTelemetry brings OpenTelemetry up (ADR 0007) and returns the flush to defer. Export is
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

// observeOutbox registers the outbox backlog and lag gauges over read (ruling P4-R15, asrs-control
// 007bf7b parity); a registration failure costs the gauges, never the boot.
func observeOutbox(log *slog.Logger, m *telemetry.TransportMetrics, read telemetry.OutboxBacklogReader) {
	if m == nil {
		return
	}
	if _, err := m.ObserveOutbox(read); err != nil {
		log.Warn("outbox gauges unavailable", "error", err)
	}
}

// newLoggerTo is the JSON logger on w at level, stamping trace_id/span_id on every record
// written under a span.
func newLoggerTo(w io.Writer, level string) *slog.Logger {
	lv, _ := telemetry.ParseLogLevel(level) // validated by loadConfig; the boot logger passes "info"
	return slog.New(telemetry.WithTraceContext(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: lv})))
}
