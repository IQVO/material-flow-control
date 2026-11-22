//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/IQVO/material-flow-control/internal/adapters/outbound/postgres"
	"github.com/IQVO/material-flow-control/internal/application/ports"
	"github.com/IQVO/material-flow-control/internal/domain/containertrack"
	"github.com/IQVO/material-flow-control/internal/domain/stationqueue"
	"github.com/IQVO/material-flow-control/internal/testing/pgtest"
)

// Issue #3 (ADR 0008): retention of container_moves, ended transport orders (with their legs), DONE/FAILED
// subsystem_commands and published outbox rows. Every purge deletes only terminal rows older than its window, never
// a row a live order or a live aggregate still references, and loops batches until nothing is left. ruling P4-R22:
// no purge ever deletes a transport_requests record (the tombstone a retried transport_request_id is answered from).

var (
	retNow    = time.Now().UTC()
	retCutoff = retNow.Add(-30 * 24 * time.Hour)
	retOld    = retCutoff.Add(-time.Hour)
	retRecent = retCutoff.Add(time.Hour)
)

func exec(t *testing.T, pool *postgres.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

func ids(t *testing.T, pool *postgres.Pool, sql string) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(), sql)
	if err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	slices.Sort(out)
	return out
}

func wantIDs(t *testing.T, what string, got []string, want ...string) {
	t.Helper()
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("%s left = %v, want %v", what, got, want)
	}
}

// seedOrder writes an order row, one leg and (for a request order) its ACCEPTED request record.
func seedOrder(t *testing.T, pool *postgres.Pool, id, status string, updated time.Time) {
	t.Helper()
	exec(t, pool, `INSERT INTO transport_requests (transport_request_id, container_id, outcome, transport_order_id, record, recorded_at)
		VALUES ($1, $2, 'ACCEPTED', $3, '{}', $4)`, "REQ-"+id, "C-"+id, id, updated)
	exec(t, pool, `INSERT INTO transport_orders (transport_order_id, transport_request_id, container_id, status, internal, snapshot,
		version, accepted_at, updated_at) VALUES ($1, $2, $3, $4, false, '{}', 1, $5, $5)`, id, "REQ-"+id, "C-"+id, status, updated)
	exec(t, pool, `INSERT INTO transport_legs (leg_ref, transport_order_id) VALUES ($1, $2)`, "LEG-"+id, id)
}

func purge(t *testing.T, p *postgres.Retention, before time.Time) int64 {
	t.Helper()
	n, err := p.Purge(context.Background(), before)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	return n
}

// ruling P4-R22: the order goes with its legs; its ACCEPTED request record stays, so a retry of its
// transport_request_id is answered expired (410) and never accepted again.
func TestOrderRetentionPurgesEndedOrdersOlderThanTheWindowWithTheirLegsButKeepsTheirRequestRecords(t *testing.T) {
	pool := pgtest.Pool(t)
	seedOrder(t, pool, "O-OLD-DONE", "COMPLETED", retOld)
	seedOrder(t, pool, "O-OLD-FAILED", "FAILED", retOld)
	seedOrder(t, pool, "O-RECENT-DONE", "COMPLETED", retRecent)
	seedOrder(t, pool, "O-OLD-LIVE", "IN_PROGRESS", retOld)
	if n := purge(t, postgres.NewOrderRetention(pool, 100), retCutoff); n != 2 {
		t.Fatalf("purged %d orders, want 2", n)
	}
	wantIDs(t, "orders", ids(t, pool, `SELECT transport_order_id FROM transport_orders`), "O-RECENT-DONE", "O-OLD-LIVE")
	wantIDs(t, "legs", ids(t, pool, `SELECT leg_ref FROM transport_legs`), "LEG-O-RECENT-DONE", "LEG-O-OLD-LIVE")
	wantIDs(t, "request records", ids(t, pool, `SELECT transport_request_id FROM transport_requests`),
		"REQ-O-OLD-DONE", "REQ-O-OLD-FAILED", "REQ-O-RECENT-DONE", "REQ-O-OLD-LIVE")
}

func TestOrderRetentionKeepsAnEndedOrderALiveRowStillReferences(t *testing.T) {
	pool := pgtest.Pool(t)
	ctx := context.Background()
	for _, id := range []string{"O-PENDING-CMD", "O-ACTIVE", "O-CLARIFY", "O-OCCUPANT", "O-WAITING", "O-FREE"} {
		seedOrder(t, pool, id, "COMPLETED", retOld)
	}
	exec(t, pool, `INSERT INTO subsystem_commands (command_id, transport_order_id, leg_ref, kind, idempotency_key, status, created_at,
		next_attempt_at, deadline) VALUES ('CMD-1', 'O-PENDING-CMD', 'LEG-O-PENDING-CMD', 'CANCEL_LEG', 'K-1', 'PENDING', $1, $1, $1)`, retOld)
	tracks := postgres.NewTracks(pool)
	if err := tracks.Save(ctx, containertrack.Restore(containertrack.Snapshot{ContainerID: "C-A",
		Position: containertrack.Position{Kind: containertrack.AtStation, Code: "ST1"}, ActiveOrderID: "O-ACTIVE", UpdatedAt: retOld})); err != nil {
		t.Fatal(err)
	}
	if err := tracks.Save(ctx, containertrack.Restore(containertrack.Snapshot{ContainerID: "C-B",
		Position:      containertrack.Position{Kind: containertrack.AtClarification, Code: "CLR1"},
		Clarification: &containertrack.Clarification{Reason: containertrack.ReasonNoRead, OrderID: "O-CLARIFY", Since: retOld}, UpdatedAt: retOld})); err != nil {
		t.Fatal(err)
	}
	if err := postgres.NewStations(pool).Save(ctx, stationqueue.Restore(stationqueue.Snapshot{StationID: "ST1", BufferLimit: 2, NextSeq: 2,
		Occupants: []stationqueue.Occupant{{OrderID: "O-OCCUPANT", ContainerID: "C-O-OCCUPANT", Seq: 1, AdmittedAt: retOld}},
		Waiting:   []stationqueue.Entry{{OrderID: "O-WAITING", ContainerID: "C-O-WAITING", ReadyAt: retOld}}})); err != nil {
		t.Fatal(err)
	}
	if n := purge(t, postgres.NewOrderRetention(pool, 100), retCutoff); n != 1 {
		t.Fatalf("purged %d orders, want only O-FREE", n)
	}
	wantIDs(t, "orders", ids(t, pool, `SELECT transport_order_id FROM transport_orders`),
		"O-PENDING-CMD", "O-ACTIVE", "O-CLARIFY", "O-OCCUPANT", "O-WAITING")
}

// ruling P4-R22: REJECTED request records are kept too (a retry is answered its 422 forever): no policy purges
// transport_requests, whatever its age.
func TestNoRetentionPolicyDeletesARequestRecord(t *testing.T) {
	pool := pgtest.Pool(t)
	seedOrder(t, pool, "O-OLD-DONE", "COMPLETED", retOld)
	exec(t, pool, `INSERT INTO transport_requests (transport_request_id, container_id, outcome, record, recorded_at)
		VALUES ('REJ-OLD', 'C-1', 'REJECTED', '{}', $1)`, retOld)
	for _, p := range []*postgres.Retention{postgres.NewOrderRetention(pool, 100), postgres.NewContainerMoveRetention(pool, 100, 1),
		postgres.NewCommandRetention(pool, 100), postgres.NewOutboxRetention(pool, 100)} {
		purge(t, p, retNow)
	}
	wantIDs(t, "request records", ids(t, pool, `SELECT transport_request_id FROM transport_requests`), "REJ-OLD", "REQ-O-OLD-DONE")
	wantIDs(t, "orders", ids(t, pool, `SELECT transport_order_id FROM transport_orders`))
}

func seedMove(t *testing.T, pool *postgres.Pool, container string, at time.Time) {
	t.Helper()
	exec(t, pool, `INSERT INTO container_moves (container_id, move, moved_at) VALUES ($1, jsonb_build_object('At', $2::timestamptz), $2)`,
		container, at)
}

func TestContainerMoveRetentionKeepsTheLatestMovesOfEveryContainerAndTheMovesOfALiveOrder(t *testing.T) {
	pool := pgtest.Pool(t)
	for i := range 5 { // C-BUSY: five old moves; only the latest two stay
		seedMove(t, pool, "C-BUSY", retOld.Add(-time.Duration(5-i)*time.Minute))
	}
	seedMove(t, pool, "C-RESTING", retOld) // fewer moves than kept: stays
	// C-RECENT: one old move, then two recent ones: the old one is third newest and goes.
	seedMove(t, pool, "C-RECENT", retOld)
	seedMove(t, pool, "C-RECENT", retRecent)
	seedMove(t, pool, "C-RECENT", retRecent)
	for range 3 { // C-FRESH: three moves inside the window: all stay, latest two or not
		seedMove(t, pool, "C-FRESH", retRecent)
	}
	for range 3 { // C-MOVING: three old moves, but a live order moves C-MOVING: all stay
		seedMove(t, pool, "C-MOVING", retOld)
	}
	exec(t, pool, `INSERT INTO transport_orders (transport_order_id, container_id, status, internal, snapshot, version, accepted_at)
		VALUES ('O-LIVE', 'C-MOVING', 'IN_PROGRESS', true, '{}', 1, $1)`, retOld)
	if n := purge(t, postgres.NewContainerMoveRetention(pool, 100, 2), retCutoff); n != 4 {
		t.Fatalf("purged %d moves, want 4 (three of C-BUSY, one of C-RECENT)", n)
	}
	got := ids(t, pool, `SELECT container_id || ':' || count(*) FROM container_moves GROUP BY container_id`)
	wantIDs(t, "moves per container", got, "C-BUSY:2", "C-FRESH:3", "C-MOVING:3", "C-RECENT:2", "C-RESTING:1")
	newest := ids(t, pool, `SELECT to_char(moved_at AT TIME ZONE 'UTC', 'HH24:MI') FROM container_moves WHERE container_id = 'C-BUSY'`)
	wantIDs(t, "C-BUSY moves", newest, retOld.Add(-2*time.Minute).Format("15:04"), retOld.Add(-time.Minute).Format("15:04"))
}

func TestCommandRetentionPurgesOnlyOldFinishedCommandsOfEndedOrders(t *testing.T) {
	pool := pgtest.Pool(t)
	seedOrder(t, pool, "O-LIVE", "IN_PROGRESS", retOld)
	seedOrder(t, pool, "O-ENDED", "COMPLETED", retOld)
	seedOrder(t, pool, "O-FAILED", "FAILED", retOld) // carry (retention review): a FAILED order has ended too (mutant M5)
	for _, c := range []struct {
		id, order, status string
		at                time.Time
	}{{"OLD-DONE", "O-ENDED", "DONE", retOld}, {"OLD-FAILED", "O-ENDED", "FAILED", retOld}, {"OLD-PENDING", "O-ENDED", "PENDING", retOld},
		{"RECENT-DONE", "O-ENDED", "DONE", retRecent}, {"LIVE-DONE", "O-LIVE", "DONE", retOld}, {"GONE-ORDER-DONE", "O-PURGED", "DONE", retOld},
		{"FAILED-ORDER-DONE", "O-FAILED", "DONE", retOld}} {
		exec(t, pool, `INSERT INTO subsystem_commands (command_id, transport_order_id, leg_ref, kind, idempotency_key, status, created_at,
			next_attempt_at, deadline) VALUES ($1, $2, 'LEG', 'REQUEST_LEG', $1, $3, $4, $4, $4)`, c.id, c.order, c.status, c.at)
	}
	if n := purge(t, postgres.NewCommandRetention(pool, 100), retCutoff); n != 4 {
		t.Fatalf("purged %d commands, want 4", n)
	}
	wantIDs(t, "commands", ids(t, pool, `SELECT command_id FROM subsystem_commands`), "OLD-PENDING", "RECENT-DONE", "LIVE-DONE")
}

func seedOutbox(t *testing.T, pool *postgres.Pool, id string, published *time.Time, attempts int) {
	t.Helper()
	exec(t, pool, `INSERT INTO outbox_events (event_id, topic, event_type, subject, dataschema, value, created_at, published_at, attempts)
		VALUES ($1, 't', 'e', 's', 'd', '\x00', $2, $3, $4)`, id, retOld.Add(-time.Hour), published, attempts)
}

func TestOutboxRetentionPurgesOnlyPublishedRowsOlderThanTheWindow(t *testing.T) {
	pool := pgtest.Pool(t)
	seedOutbox(t, pool, "PUB-OLD", &retOld, 1)
	seedOutbox(t, pool, "PUB-RECENT", &retRecent, 1)
	seedOutbox(t, pool, "UNPUB", nil, 0)
	seedOutbox(t, pool, "UNPUB-CAPPED", nil, 20) // attempt-capped: kept for the runbook's re-arm
	if n := purge(t, postgres.NewOutboxRetention(pool, 100), retCutoff); n != 1 {
		t.Fatalf("purged %d outbox rows, want 1", n)
	}
	wantIDs(t, "outbox rows", ids(t, pool, `SELECT event_id FROM outbox_events`), "PUB-RECENT", "UNPUB", "UNPUB-CAPPED")
}

func TestEveryRetentionLoopsItsBatchesUntilNothingIsLeft(t *testing.T) {
	pool := pgtest.Pool(t)
	for i := range 25 {
		seedOutbox(t, pool, fmt.Sprintf("PUB-%02d", i), &retOld, 1)
		seedMove(t, pool, fmt.Sprintf("C-%02d", i%5), retOld)
		exec(t, pool, `INSERT INTO subsystem_commands (command_id, transport_order_id, leg_ref, kind, idempotency_key, status, created_at,
			next_attempt_at, deadline) VALUES ($1, 'O-X', 'LEG', 'REQUEST_LEG', $1, 'DONE', $2, $2, $2)`, fmt.Sprintf("CMD-%02d", i), retOld)
	}
	for i := range 7 {
		seedOrder(t, pool, fmt.Sprintf("O-%02d", i), "COMPLETED", retOld)
	}
	for name, c := range map[string]struct {
		p     *postgres.Retention
		want  int64
		table string
	}{"outbox": {postgres.NewOutboxRetention(pool, 4), 25, "outbox_events"},
		"moves":    {postgres.NewContainerMoveRetention(pool, 3, 1), 20, "container_moves"},
		"commands": {postgres.NewCommandRetention(pool, 10), 25, "subsystem_commands"},
		"orders":   {postgres.NewOrderRetention(pool, 2), 7, "transport_orders"}} {
		if n := purge(t, c.p, retCutoff); n != c.want {
			t.Errorf("%s purged %d rows over its batches, want %d", name, n, c.want)
		}
		if n := purge(t, c.p, retCutoff); n != 0 {
			t.Errorf("%s: a second pass purged %d rows, want 0", name, n)
		}
		if c.p.Table() != c.table {
			t.Errorf("%s table = %q, want %q", name, c.p.Table(), c.table)
		}
	}
}

// The order purge takes the flow lock (decision D19) first, so it never interleaves with a unit of work that reads an
// order or its request record (a RequestTransport replay, a late fact) and then expects it to be there.
func TestOrderRetentionWaitsForTheFlowLock(t *testing.T) {
	pool := pgtest.Pool(t)
	seedOrder(t, pool, "O-OLD", "COMPLETED", retOld)
	ctx := context.Background()
	held, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- postgres.NewUnitOfWork(pool).Do(ctx, func(ctx context.Context) error {
			if err := postgres.NewFlowLock(pool).Lock(ctx); err != nil {
				return err
			}
			close(held)
			<-release
			return nil
		})
	}()
	<-held
	type result struct {
		n   int64
		err error
	}
	purged := make(chan result, 1)
	go func() {
		n, err := postgres.NewOrderRetention(pool, 100).Purge(ctx, retCutoff)
		purged <- result{n, err}
	}()
	select {
	case r := <-purged:
		close(release)
		t.Fatalf("the order purge returned (%d rows, %v) while another unit of work held the flow lock", r.n, r.err)
	case <-time.After(500 * time.Millisecond):
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if r := <-purged; r.err != nil || r.n != 1 {
		t.Fatalf("after the lock was released: purged %d, err %v; want 1, nil", r.n, r.err)
	}
}

// carry (retention review): what the expired answer rests on (ruling P4-R22). After the order purge the request
// record still reads, and its order reads ports.ErrNotFound, which the use cases turn into ErrTransportExpired.
func TestAfterTheOrderPurgeTheRecordReadsAndItsOrderIsNotFound(t *testing.T) {
	pool := pgtest.Pool(t)
	ctx := context.Background()
	seedOrder(t, pool, "O-OLD", "COMPLETED", retOld)
	purge(t, postgres.NewOrderRetention(pool, 100), retCutoff)
	if _, err := postgres.NewRequests(pool).Get(ctx, "REQ-O-OLD"); err != nil {
		t.Fatalf("the record of a purged order = %v, want it kept", err)
	}
	if _, err := postgres.NewOrders(pool).Get(ctx, "O-OLD"); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("the purged order = %v, want ports.ErrNotFound", err)
	}
}
