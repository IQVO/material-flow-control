package main

import (
	"context"
	"io"
	"log/slog"
	"time"

	"github.com/IQVO/material-flow-control/internal/adapters/outbound/telemetry"
)

// telemetryFlushTimeout bounds the final export so a dead Collector cannot stretch a shutdown.
const telemetryFlushTimeout = 5 * time.Second

// startTelemetry brings OpenTelemetry up (ADR 0007; an empty endpoint installs nothing) and
// returns the flush to defer. It must run before newRouter: otelchimetric binds the global
// MeterProvider at construction.
func startTelemetry(log *slog.Logger, serviceName, serviceVersion, endpoint string) (func(), error) {
	shutdown, err := telemetry.Setup(context.Background(), serviceName, serviceVersion, endpoint)
	if err != nil {
		return nil, err
	}
	if endpoint == "" {
		log.Info("telemetry disabled: OTEL_EXPORTER_OTLP_ENDPOINT is not set", "service_name", serviceName)
	} else {
		log.Info("telemetry configured", "service_name", serviceName, "otlp_endpoint", endpoint)
	}
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), telemetryFlushTimeout)
		defer cancel()
		if err := shutdown(ctx); err != nil {
			log.Warn("telemetry flush failed on shutdown", "error", err)
		}
	}, nil
}

// newLogger is the JSON logger on w at level, stamping trace_id/span_id under a span.
func newLogger(w io.Writer, level string) *slog.Logger {
	lv, _ := telemetry.ParseLogLevel(level) // validated by loadConfig; the boot logger passes "info"
	return slog.New(telemetry.WithTraceContext(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: lv})))
}
