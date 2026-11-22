//go:build integration

package postgres_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/IQVO/material-flow-control/internal/adapters/outbound/postgres"
	"github.com/IQVO/material-flow-control/internal/testing/pgtest"
)

func TestTheFlowLockSerializesUnitsOfWork(t *testing.T) {
	pool := pgtest.Pool(t)
	uow, lock := postgres.NewUnitOfWork(pool), postgres.NewFlowLock(pool)
	ctx := context.Background()
	var inside, maxInside atomic.Int32
	done := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			done <- uow.Do(ctx, func(ctx context.Context) error {
				if err := lock.Lock(ctx); err != nil {
					return err
				}
				n := inside.Add(1)
				if n > maxInside.Load() {
					maxInside.Store(n)
				}
				time.Sleep(300 * time.Millisecond)
				inside.Add(-1)
				return nil
			})
		}()
	}
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if maxInside.Load() != 1 {
		t.Fatalf("%d units of work held the flow lock at once", maxInside.Load())
	}
	if err := lock.Lock(ctx); err == nil {
		t.Fatal("the flow lock outside a unit of work must be an error")
	}
}
