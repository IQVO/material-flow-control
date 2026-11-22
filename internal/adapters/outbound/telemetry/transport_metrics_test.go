package telemetry_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/IQVO/material-flow-control/internal/adapters/outbound/telemetry"
)

func TestEveryEndingIsCountedOnceWithPurposeOutcomeAndReason(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	prev := otel.GetMeterProvider()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	t.Cleanup(func() { otel.SetMeterProvider(prev) })
	m, err := telemetry.NewTransportMetrics()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	m.TransportEnded(ctx, "PICK", "completed", "", 3*time.Minute)
	m.TransportEnded(ctx, "PICK", "completed", "", time.Minute)
	m.TransportEnded(ctx, "PUTAWAY", "failed", "SUBSYSTEM_FAULT", time.Minute)
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &rm); err != nil {
		t.Fatal(err)
	}
	got := map[string]int64{}
	for _, sm := range rm.ScopeMetrics {
		for _, mt := range sm.Metrics {
			if mt.Name != "material_flow_control.transports.ended" {
				continue
			}
			if mt.Unit != "{transport}" {
				t.Fatalf("unit = %s", mt.Unit)
			}
			for _, dp := range mt.Data.(metricdata.Sum[int64]).DataPoints {
				p, _ := dp.Attributes.Value(attribute.Key("purpose"))
				o, _ := dp.Attributes.Value(attribute.Key("outcome"))
				r, _ := dp.Attributes.Value(attribute.Key("reason"))
				got[p.AsString()+"/"+o.AsString()+"/"+r.AsString()] = dp.Value
			}
		}
	}
	want := map[string]int64{"PICK/completed/": 2, "PUTAWAY/failed/SUBSYSTEM_FAULT": 1}
	if len(got) != len(want) || got["PICK/completed/"] != 2 || got["PUTAWAY/failed/SUBSYSTEM_FAULT"] != 1 {
		t.Fatalf("points = %v, want %v", got, want)
	}
	var nilMetrics *telemetry.TransportMetrics
	nilMetrics.TransportEnded(ctx, "PICK", "completed", "", 0)
}

// ruling P4-R15: the outbox gauges (asrs-control ObserveOutbox parity, names per OutboxRepo.Backlog).
func TestTheOutboxGaugesObserveTheBacklogAndLag(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	prev := otel.GetMeterProvider()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	t.Cleanup(func() { otel.SetMeterProvider(prev) })
	m, err := telemetry.NewTransportMetrics()
	if err != nil {
		t.Fatal(err)
	}
	reg, err := m.ObserveOutbox(func(context.Context) (int64, time.Duration, error) { return 7, 90 * time.Second, nil })
	if err != nil {
		t.Fatalf("ObserveOutbox: %v", err)
	}
	defer func() { _ = reg.Unregister() }()
	got := collectGauges(t, reader)
	backlog, ok := got["material_flow_control.outbox.backlog"].(metricdata.Gauge[int64])
	if !ok || len(backlog.DataPoints) != 1 || backlog.DataPoints[0].Value != 7 {
		t.Errorf("backlog = %+v, want one point of 7", got["material_flow_control.outbox.backlog"])
	}
	lag, ok := got["material_flow_control.outbox.lag"].(metricdata.Gauge[float64])
	if !ok || len(lag.DataPoints) != 1 || lag.DataPoints[0].Value != 90 {
		t.Errorf("lag = %+v, want one point of 90 seconds", got["material_flow_control.outbox.lag"])
	}
}

// A failed backlog read observes nothing: a database outage is a gap in the series, never a zero backlog.
func TestTheOutboxGaugesObserveNothingWhenTheReadFails(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	prev := otel.GetMeterProvider()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	t.Cleanup(func() { otel.SetMeterProvider(prev) })
	m, err := telemetry.NewTransportMetrics()
	if err != nil {
		t.Fatal(err)
	}
	reg, err := m.ObserveOutbox(func(context.Context) (int64, time.Duration, error) { return 0, 0, errors.New("db down") })
	if err != nil {
		t.Fatalf("ObserveOutbox: %v", err)
	}
	defer func() { _ = reg.Unregister() }()
	if g, ok := collectGauges(t, reader)["material_flow_control.outbox.backlog"].(metricdata.Gauge[int64]); ok && len(g.DataPoints) > 0 {
		t.Errorf("a failed read must observe no backlog point, got %+v", g.DataPoints)
	}
}

func collectGauges(t *testing.T, r *sdkmetric.ManualReader) map[string]metricdata.Aggregation {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := r.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}
	out := map[string]metricdata.Aggregation{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			out[m.Name] = m.Data
		}
	}
	return out
}
