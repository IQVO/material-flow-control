//go:build integration

package http_test

// carry (Task 13 review): no idempotency write before the use case's flow lock, proved over the REAL Postgres
// unit of work, flow lock (SELECT ... FOR UPDATE) and key store. A burst of requests under one key must end
// without a deadlock or a 500: the first runs, every concurrent duplicate waits on the flow lock, loses the key
// at save time and rolls back (or replays), so one record, one event (two outbox rows) and one key row exist.

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"testing"
	"time"

	mfchttp "github.com/IQVO/material-flow-control/internal/adapters/inbound/http"
	"github.com/IQVO/material-flow-control/internal/adapters/outbound/events"
	"github.com/IQVO/material-flow-control/internal/adapters/outbound/memory"
	"github.com/IQVO/material-flow-control/internal/adapters/outbound/postgres"
	"github.com/IQVO/material-flow-control/internal/application/usecases"
	"github.com/IQVO/material-flow-control/internal/config"
	ct "github.com/IQVO/material-flow-control/internal/domain/containertrack"
	"github.com/IQVO/material-flow-control/internal/testing/pgtest"
)

type pgAPI struct {
	h     http.Handler
	count func(t *testing.T, sql string) int
	save  func(t *testing.T, tr *ct.Track)
}

func newPostgresAPI(t *testing.T) *pgAPI {
	t.Helper()
	pool := pgtest.Pool(t)
	l, err := config.LoadLayout("../../../../config/flow-layout.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	uow, clock := postgres.NewUnitOfWork(pool), memory.NewClock(t0)
	tracks := postgres.NewTracks(pool)
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc, err := usecases.NewService(l, usecases.DefaultConfig(), usecases.Deps{UoW: uow, Lock: postgres.NewFlowLock(pool),
		Requests: postgres.NewRequests(pool), Orders: postgres.NewOrders(pool), Tracks: tracks, Stations: postgres.NewStations(pool),
		Capacities: postgres.NewCapacities(pool), Units: postgres.NewUnits(pool), Commands: postgres.NewCommands(pool),
		Inbox: postgres.NewProcessedEvents(pool), Sink: events.NewSink(postgres.NewOutboxRepo(pool), memory.NewIDs("ev").NewID),
		Asrs: memory.NewAsrs(), Amr: memory.NewAmr(), Clock: clock, IDs: memory.NewIDs("to"),
		Metrics: &memory.Metrics{}, Kick: &memory.Kicker{}, Log: discard})
	if err != nil {
		t.Fatal(err)
	}
	idem := mfchttp.NewIdempotency(postgres.NewIdempotencyStore(pool), uow, clock)
	return &pgAPI{
		h: mfchttp.NewRouter(&mfchttp.Server{Service: svc, Idempotency: idem.Middleware}, discard),
		count: func(t *testing.T, sql string) int {
			t.Helper()
			var n int
			if err := pool.QueryRow(context.Background(), sql).Scan(&n); err != nil {
				t.Fatalf("%s: %v", sql, err)
			}
			return n
		},
		save: func(t *testing.T, tr *ct.Track) {
			t.Helper()
			if err := tracks.Save(context.Background(), tr); err != nil {
				t.Fatal(err)
			}
		},
	}
}

// burst sends bodies[i] concurrently under one key and returns each status and body; it fails on a wedge.
func (p *pgAPI) burst(t *testing.T, key string, bodies []any) ([]int, []map[string]any) {
	t.Helper()
	a := &api{t: t, h: p.h}
	codes, outs := make([]int, len(bodies)), make([]map[string]any, len(bodies))
	var wg sync.WaitGroup
	for i := range bodies {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rec, body := a.do(http.MethodPost, "/transport-requests", key, bodies[i])
			codes[i], outs[i] = rec.Code, body
		}(i)
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatalf("the burst under key %s did not finish in 30 s (codes so far %v): a lock-order wedge", key, codes)
	}
	return codes, outs
}

func TestABurstOfOneRequestUnderOneKeyRunsOnceOverPostgres(t *testing.T) {
	p := newPostgresAPI(t)
	tr := ct.New("TOTE-1", t0)
	tr.MoveTo(ct.Position{Kind: ct.InAsrs, Code: asrsLoc}, t0)
	p.save(t, tr)
	bodies := make([]any, 6)
	for i := range bodies {
		bodies[i] = pick("task-1.1", "TOTE-1")
	}
	codes, outs := p.burst(t, "k-pg-same", bodies)
	for i := range codes {
		if codes[i] != http.StatusAccepted || outs[i]["transport_order_id"] != outs[0]["transport_order_id"] {
			t.Fatalf("answers %v (%v), want six 202 naming one order", codes, outs)
		}
	}
	for sql, want := range map[string]int{
		`SELECT count(*) FROM transport_requests`: 1, `SELECT count(*) FROM transport_orders`: 1,
		`SELECT count(*) FROM idempotency_keys`: 1, `SELECT count(*) FROM outbox_events`: 2,
	} {
		if got := p.count(t, sql); got != want {
			t.Fatalf("%s = %d, want %d", sql, got, want)
		}
	}
}

func TestConcurrentRequestsWithOtherBodiesUnderOneKeyCommitOnlyTheFirstOverPostgres(t *testing.T) {
	p := newPostgresAPI(t)
	ids := []string{"task-9.1", "task-9.2", "task-9.3", "task-9.4", "task-9.5", "task-9.6"}
	bodies := make([]any, len(ids))
	for i, id := range ids {
		bodies[i] = pick(id, "TOTE-9")
	}
	codes, outs := p.burst(t, "k-pg-other", bodies)
	rejected, reused := 0, 0
	for i, c := range codes {
		switch {
		case c == http.StatusUnprocessableEntity && outs[i]["code"] == "unknown-container":
			rejected++
		case c == http.StatusConflict && outs[i]["code"] == "idempotency-key-reused":
			reused++
		}
	}
	if rejected != 1 || reused != len(ids)-1 {
		t.Fatalf("answers %v (%v), want one 422 and %d 409 idempotency-key-reused", codes, outs, len(ids)-1)
	}
	for sql, want := range map[string]int{
		`SELECT count(*) FROM transport_requests`: 1, `SELECT count(*) FROM idempotency_keys`: 1,
		`SELECT count(*) FROM outbox_events WHERE event_type LIKE '%TransportRejected'`: 2,
	} {
		if got := p.count(t, sql); got != want {
			t.Fatalf("%s = %d, want %d: only the first request's writes commit", sql, got, want)
		}
	}
}
