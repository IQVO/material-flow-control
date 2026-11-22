//go:build integration

package main

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/IQVO/material-flow-control/internal/domain/transportrequest"
	"github.com/IQVO/material-flow-control/internal/testing/pgtest"
)

// ruling P4-R15: wire registers the outbox backlog and lag gauges over the real outbox (asrs-control
// observeOutbox parity): an accepted request leaves its two unpublished rows (no relay runs here), and
// a collection reads them.
func TestWireRegistersTheOutboxBacklogAndLagGauges(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	prev := otel.GetMeterProvider()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	t.Cleanup(func() { otel.SetMeterProvider(prev) })
	cfg, err := loadConfig(env(map[string]string{"DATABASE_URL": pgtest.URL(t), "KAFKA_BROKERS": "127.0.0.1:1",
		"ASRS_CONTROL_URL": "http://127.0.0.1:1", "AMR_FLEET_CONTROL_URL": "http://127.0.0.1:1",
		"MFC_LAYOUT_FILE": "../../config/flow-layout.example.yaml"}))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	a, err := wire(ctx, cfg, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeWired(a) })
	if _, err := a.svc.RequestTransport(ctx, transportrequest.Request{ID: "rq-g", ContainerID: "TOTE-G", Source: "WH1-RCV-DEC1",
		Destination: "WH1-PCK-PK1", Purpose: transportrequest.PurposeOutbound, Priority: transportrequest.PriorityNormal,
		DueBy: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &rm); err != nil {
		t.Fatal(err)
	}
	got := map[string]metricdata.Aggregation{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			got[m.Name] = m.Data
		}
	}
	backlog, ok := got["material_flow_control.outbox.backlog"].(metricdata.Gauge[int64])
	if !ok || len(backlog.DataPoints) != 1 || backlog.DataPoints[0].Value < 1 {
		t.Fatalf("backlog = %+v, want one point counting the unpublished rows", got["material_flow_control.outbox.backlog"])
	}
	lag, ok := got["material_flow_control.outbox.lag"].(metricdata.Gauge[float64])
	if !ok || len(lag.DataPoints) != 1 || lag.DataPoints[0].Value < 0 {
		t.Fatalf("lag = %+v, want one point in seconds", got["material_flow_control.outbox.lag"])
	}
}
