package main

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/IQVO/material-flow-control/internal/adapters/outbound/telemetry"
)

// ruling P4-R15: observeOutbox registers both gauges over the reader it is given; nil metrics are a no-op.
func TestObserveOutboxRegistersBothGaugesOverTheReader(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	prev := otel.GetMeterProvider()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	t.Cleanup(func() { otel.SetMeterProvider(prev) })
	log := slog.New(slog.DiscardHandler)
	observeOutbox(log, nil, func(context.Context) (int64, time.Duration, error) { t.Fatal("nil metrics read"); return 0, 0, nil })
	m, err := telemetry.NewTransportMetrics()
	if err != nil {
		t.Fatal(err)
	}
	observeOutbox(log, m, func(context.Context) (int64, time.Duration, error) { return 3, 2 * time.Second, nil })
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, sm := range rm.ScopeMetrics {
		for _, mt := range sm.Metrics {
			seen[mt.Name] = true
		}
	}
	if !seen["material_flow_control.outbox.backlog"] || !seen["material_flow_control.outbox.lag"] {
		t.Fatalf("instruments = %v, want the outbox backlog and lag gauges", seen)
	}
}
