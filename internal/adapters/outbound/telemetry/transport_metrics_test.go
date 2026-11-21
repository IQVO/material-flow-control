package telemetry_test

import (
	"context"
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
