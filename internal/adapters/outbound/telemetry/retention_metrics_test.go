package telemetry_test

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/IQVO/material-flow-control/internal/adapters/outbound/telemetry"
)

// Issue #3 (ADR 0008): material_flow_control.retention.purged counts the rows every retention pass deleted, by table.
func TestThePurgedCounterAddsTheRowsOfEveryPassByTable(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	prev := otel.GetMeterProvider()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	t.Cleanup(func() { otel.SetMeterProvider(prev) })
	m, err := telemetry.NewTransportMetrics()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	m.RowsPurged(ctx, "container_moves", 40)
	m.RowsPurged(ctx, "container_moves", 2)
	m.RowsPurged(ctx, "outbox_events", 7)
	m.RowsPurged(ctx, "transport_orders", 0) // a pass that found nothing still shows the series
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &rm); err != nil {
		t.Fatal(err)
	}
	got := map[string]int64{}
	for _, sm := range rm.ScopeMetrics {
		for _, mt := range sm.Metrics {
			if mt.Name != "material_flow_control.retention.purged" {
				continue
			}
			if mt.Unit != "{row}" {
				t.Fatalf("unit = %s", mt.Unit)
			}
			for _, dp := range mt.Data.(metricdata.Sum[int64]).DataPoints {
				if dp.Attributes.Len() != 1 {
					t.Fatalf("attributes = %v, want only table", dp.Attributes.ToSlice())
				}
				table, _ := dp.Attributes.Value(attribute.Key("table"))
				got[table.AsString()] = dp.Value
			}
		}
	}
	if len(got) != 3 || got["container_moves"] != 42 || got["outbox_events"] != 7 || got["transport_orders"] != 0 {
		t.Fatalf("points = %v, want container_moves 42, outbox_events 7, transport_orders 0", got)
	}
	var nilMetrics *telemetry.TransportMetrics
	nilMetrics.RowsPurged(ctx, "outbox_events", 1)
}
