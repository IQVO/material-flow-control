package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"slices"
	"testing"
	"time"
)

type fakePurgeStore struct {
	n      int64
	err    error
	before []time.Time
}

func (s *fakePurgeStore) Purge(_ context.Context, before time.Time) (int64, error) {
	s.before = append(s.before, before)
	return s.n, s.err
}

type recordedPurges map[string]int64

func (r recordedPurges) RowsPurged(_ context.Context, table string, n int64) { r[table] += n }

// Issue #3 (ADR 0008): every purge pass counts each table's purged rows (a failed pass counts nothing) and hands
// every store the cutoff of its own window.
func TestAPurgePassCountsEveryTablesRowsAndUsesEachWindow(t *testing.T) {
	moves, outbox, broken := &fakePurgeStore{n: 3}, &fakePurgeStore{n: 0}, &fakePurgeStore{n: 9, err: errors.New("db down")}
	got := recordedPurges{}
	a := &app{log: slog.New(slog.NewJSONHandler(io.Discard, nil)), purged: got, purgers: []purger{
		{"container_moves", moves, 48 * time.Hour}, {"outbox_events", outbox, time.Hour}, {"subsystem_commands", broken, time.Hour}}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // one pass, then the loop sees the cancelled context and returns
	start := time.Now().UTC()
	a.purge(ctx)
	if want := (recordedPurges{"container_moves": 3, "outbox_events": 0}); !maps.Equal(got, want) {
		t.Fatalf("counted %v, want %v (a failed pass counts nothing)", got, want)
	}
	if len(moves.before) != 1 || len(outbox.before) != 1 || len(broken.before) != 1 {
		t.Fatalf("passes = %d/%d/%d, want one each", len(moves.before), len(outbox.before), len(broken.before))
	}
	if d := start.Sub(moves.before[0]); d > 48*time.Hour || d < 48*time.Hour-time.Minute {
		t.Fatalf("container_moves cutoff is %s before now, want its 48h window", d)
	}
	if d := start.Sub(outbox.before[0]); d > time.Hour || d < time.Hour-time.Minute {
		t.Fatalf("outbox_events cutoff is %s before now, want its 1h window", d)
	}
}

// Issue #3 (ADR 0008): cmd/api purges the five retention tables, orders first, each with its own configured window
// (the REJECTED request records share the order window).
func TestTheRetentionPoliciesAreWiredWithTheirWindows(t *testing.T) {
	m := full()
	m["ORDER_RETENTION_HOURS"], m["CONTAINER_MOVE_RETENTION_HOURS"], m["COMMAND_RETENTION_HOURS"] = "100", "200", "300"
	m["OUTBOX_RETENTION_HOURS"] = "400"
	cfg, err := loadConfig(env(m))
	if err != nil {
		t.Fatal(err)
	}
	got := []string{}
	for _, p := range retentionPurgers(nil, cfg) {
		got = append(got, fmt.Sprintf("%s=%s", p.name, p.keep))
	}
	want := []string{"transport_orders=100h0m0s", "transport_requests=100h0m0s", "container_moves=200h0m0s",
		"subsystem_commands=300h0m0s", "outbox_events=400h0m0s"}
	if !slices.Equal(got, want) {
		t.Fatalf("purgers = %v, want %v", got, want)
	}
}
