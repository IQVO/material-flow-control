//go:build integration

package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/IQVO/material-flow-control/internal/adapters/outbound/postgres"
	"github.com/IQVO/material-flow-control/internal/testing/pgtest"
)

// carry (retention review): the moves purge evaluates every row at most once per pass. Rows its guards keep (the
// moves of a container a live order is moving) used to be re-read by every batch of the pass, so a pass over many
// kept rows cost (kept rows) x (batches) and ran for minutes. 40k kept moves come first by moved_at, then 10k
// purgeable ones of other containers, in batches of 100: the pass deletes every purgeable move and keeps every
// guarded one inside a budget a rescanning pass (100 batches x 40k rows) cannot meet.
func TestTheMovesPurgeReadsEveryGuardedRowOncePerPass(t *testing.T) {
	pool := pgtest.Pool(t)
	// C-LIVE-0..3999: ten old moves each, oldest of all, every container moved by a live order.
	exec(t, pool, `INSERT INTO container_moves (container_id, move, moved_at)
		SELECT 'C-LIVE-' || (i % 4000), '{}', $1::timestamptz + i * interval '1 millisecond' FROM generate_series(1, 40000) i`,
		retOld.Add(-48*time.Hour))
	exec(t, pool, `INSERT INTO transport_orders (transport_order_id, container_id, status, internal, snapshot, version, accepted_at)
		SELECT 'O-LIVE-' || i, 'C-LIVE-' || i, 'IN_PROGRESS', true, '{}', 1, $1 FROM generate_series(0, 3999) i`, retOld)
	// C-IDLE-0..999: eleven old moves each, after every guarded one; the newest of each is kept (keepLatest 1).
	exec(t, pool, `INSERT INTO container_moves (container_id, move, moved_at)
		SELECT 'C-IDLE-' || (i % 1000), '{}', $1::timestamptz + i * interval '1 millisecond' FROM generate_series(1, 11000) i`,
		retOld.Add(-24*time.Hour))
	exec(t, pool, `ANALYZE`)
	start := time.Now()
	n, err := postgres.NewContainerMoveRetention(pool, 100, 1).Purge(context.Background(), retCutoff)
	took := time.Since(start)
	if err != nil || n != 10000 {
		t.Fatalf("purge = %d, %v; want the 10000 purgeable moves, no error (took %s)", n, err, took)
	}
	t.Logf("the moves pass took %s", took)
	if took > 4*time.Second {
		t.Fatalf("the moves pass took %s over 40k guarded rows, want under 4 s (each row read once per pass)", took)
	}
	got := ids(t, pool, `SELECT split_part(container_id, '-', 2) || ':' || count(*) FROM container_moves GROUP BY split_part(container_id, '-', 2)`)
	wantIDs(t, "moves by container kind", got, "IDLE:1000", "LIVE:40000")
}
