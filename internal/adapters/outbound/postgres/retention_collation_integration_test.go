//go:build integration

package postgres_test

import (
	"context"
	"testing"

	"github.com/IQVO/material-flow-control/internal/adapters/outbound/postgres"
	"github.com/IQVO/material-flow-control/internal/testing/pgtest"
)

// carry (retention review): transport_orders.transport_order_id is COLLATE "C", so a guard comparing another table's
// order id with it compares under "C" (the column's collation wins over the default). An index built under the
// default collation cannot serve that comparison, and the review's plans showed idx_container_tracks_active_order
// and idx_subsystem_commands_pending_order never used. With scans and hash/merge joins priced out, the order purge's
// guards must still look each candidate's live rows up by those indexes (an Index Cond on them, not a full scan).
func TestTheOrderPurgeGuardsLookUpTheirLiveRowsByIndex(t *testing.T) {
	url := pgtest.URL(t)
	seed, err := postgres.NewPool(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(seed.Close)
	// Live rows of other orders, enough that a probe per candidate beats reading the whole table.
	exec(t, seed, `INSERT INTO subsystem_commands (command_id, transport_order_id, leg_ref, kind, idempotency_key, status,
		created_at, next_attempt_at, deadline) SELECT 'CMD-' || i, 'X-' || i, 'L', 'REQUEST_LEG', 'K-' || i, 'PENDING', $1, $1, $1
		FROM generate_series(1, 5000) i`, retOld)
	exec(t, seed, `INSERT INTO container_tracks (container_id, position_kind, active_order_id, in_clarification, snapshot, version,
		updated_at) SELECT 'C-T-' || i, 'AT_STATION', 'X-' || i, false, '{}', 1, $1 FROM generate_series(1, 5000) i`, retOld)
	seedOrders(t, seed, 200, 100)
	var p plans
	pool := explainedPool(t, url, &p, `SET enable_seqscan = off; SET enable_hashjoin = off; SET enable_mergejoin = off`)
	if _, err := postgres.NewOrderRetention(pool, 1000).Purge(context.Background(), retCutoff); err != nil {
		t.Fatal(err)
	}
	for _, index := range []string{"idx_container_tracks_active_order", "idx_subsystem_commands_pending_order"} {
		if conds := p.indexConds(t, index); len(conds) == 0 {
			t.Errorf("no plan of the order purge looks rows up by %s (%d plans): the guard cannot use it", index, len(p.raw))
		}
	}
}
