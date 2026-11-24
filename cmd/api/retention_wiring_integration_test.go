//go:build integration

package main

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/IQVO/material-flow-control/internal/testing/pgtest"
)

// Issue #3 (ADR 0008): the wired purge loop runs the retention policies over the real, migrated database under the
// default windows, and counts every table's pass on material_flow_control.retention.purged.
func TestThePurgeLoopRunsTheRetentionPoliciesWithTheDefaultWindows(t *testing.T) {
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
	old, recent := time.Now().UTC().Add(-721*time.Hour), time.Now().UTC().Add(-719*time.Hour)
	for id, at := range map[string]time.Time{"O-OLD": old, "O-RECENT": recent} {
		mustExec(t, a, `INSERT INTO transport_orders (transport_order_id, container_id, status, internal, snapshot, version, accepted_at,
			updated_at) VALUES ($1, 'C-1', 'COMPLETED', true, '{}', 1, $2, $2)`, id, at)
		mustExec(t, a, `INSERT INTO transport_legs (leg_ref, transport_order_id) VALUES ($1 || '.1.1', $1)`, id)
	}
	for id, at := range map[string]time.Time{"E-OLD": time.Now().UTC().Add(-169 * time.Hour), "E-RECENT": time.Now().UTC().Add(-167 * time.Hour)} {
		mustExec(t, a, `INSERT INTO outbox_events (event_id, topic, event_type, subject, dataschema, value, published_at)
			VALUES ($1, 't', 'e', 's', 'd', '\x00', $2)`, id, at)
	}
	a.purgeOnce(ctx)
	for table, want := range map[string]string{"transport_orders": "O-RECENT", "outbox_events": "E-RECENT"} {
		col := map[string]string{"transport_orders": "transport_order_id", "outbox_events": "event_id"}[table]
		var left []string
		rows, err := a.pool.Query(ctx, `SELECT `+col+` FROM `+table)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				t.Fatal(err)
			}
			left = append(left, s)
		}
		rows.Close()
		if len(left) != 1 || left[0] != want {
			t.Errorf("%s left %v, want only %s", table, left, want)
		}
	}
	got := purgedPoints(t, reader)
	// ruling P4-R22: no series for transport_requests: no policy deletes a request record.
	// carry (retention review): the order's legs are counted under their own table.
	want := map[string]int64{"idempotency_keys": 0, "processed_events": 0, "transport_orders": 1, "transport_legs": 1,
		"container_moves": 0, "subsystem_commands": 0, "outbox_events": 1}
	if len(got) != len(want) {
		t.Fatalf("purged points = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("purged points = %v, want %v", got, want)
		}
	}
}

func mustExec(t *testing.T, a *app, sql string, args ...any) {
	t.Helper()
	if _, err := a.pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

func purgedPoints(t *testing.T, reader *sdkmetric.ManualReader) map[string]int64 {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	got := map[string]int64{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != "material_flow_control.retention.purged" {
				continue
			}
			for _, dp := range m.Data.(metricdata.Sum[int64]).DataPoints {
				table, _ := dp.Attributes.Value(attribute.Key("table"))
				got[table.AsString()] = dp.Value
			}
		}
	}
	return got
}
