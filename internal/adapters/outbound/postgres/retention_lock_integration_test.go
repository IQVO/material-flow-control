//go:build integration

package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/IQVO/material-flow-control/internal/adapters/outbound/postgres"
	"github.com/IQVO/material-flow-control/internal/testing/pgtest"
)

// carry (retention review): the order purge takes the flow lock BEFORE it touches an order. A unit of work holding
// the lock (decision D19) can therefore still lock the old order and its legs while the purge waits: a purge that
// deleted first and locked after (mutant M3) would hold those rows and fail the holder's NOWAIT locks.
// TestOrderRetentionWaitsForTheFlowLock alone does not tell the two apart: both wait, both purge after the release.
func TestTheOrderPurgeLocksTheFlowBeforeAnyOrderRow(t *testing.T) {
	pool := pgtest.Pool(t)
	seedOrder(t, pool, "O-OLD", "COMPLETED", retOld)
	ctx := context.Background()
	purged := make(chan error, 1)
	err := postgres.NewUnitOfWork(pool).Do(ctx, func(ctx context.Context) error {
		if err := postgres.NewFlowLock(pool).Lock(ctx); err != nil {
			return err
		}
		go func() {
			_, err := postgres.NewOrderRetention(pool, 100).Purge(context.Background(), retCutoff)
			purged <- err
		}()
		time.Sleep(500 * time.Millisecond) // the purge is running, waiting on the flow lock
		q := postgres.QuerierFor(ctx, pool)
		for _, sql := range []string{
			`SELECT transport_order_id FROM transport_orders WHERE transport_order_id = 'O-OLD' FOR UPDATE NOWAIT`,
			`SELECT leg_ref FROM transport_legs WHERE transport_order_id = 'O-OLD' FOR UPDATE NOWAIT`,
		} {
			rows, err := q.Query(ctx, sql)
			if err == nil {
				for rows.Next() {
				}
				err = rows.Err()
				rows.Close()
			}
			if err != nil {
				t.Errorf("while the purge waits on the flow lock, %q = %v: the purge holds the row", sql, err)
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Logf("the holder's unit of work: %v", err)
	}
	if err := <-purged; err != nil {
		t.Fatalf("the purge after the release: %v", err)
	}
	wantIDs(t, "orders", ids(t, pool, `SELECT transport_order_id FROM transport_orders`))
}
