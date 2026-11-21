//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/IQVO/material-flow-control/internal/adapters/outbound/postgres"
	"github.com/IQVO/material-flow-control/internal/application/outbox"
	"github.com/IQVO/material-flow-control/internal/testing/pgtest"
)

// carry (Task 13 review): PgBouncer runs pool_mode=transaction, so the session-level SET in
// NewPool's AfterConnect does not reach the server connection a later transaction runs on. The
// pool here has NO AfterConnect (what a transaction pooler leaves you): only the unit of work's
// own SET LOCAL bounds a unit waiting on the flow lock another unit holds.
func TestAUnitWaitingOnAHeldFlowLockGivesUpWithinTheTimeouts(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.BarePool(t)
	uow, lock := postgres.NewUnitOfWork(pool), postgres.NewFlowLock(pool)
	holding, release, held := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		held <- uow.Do(ctx, func(ctx context.Context) error {
			if err := lock.Lock(ctx); err != nil {
				return err
			}
			close(holding)
			<-release
			return nil
		})
	}()
	<-holding
	wctx, cancel := context.WithTimeout(ctx, 15*time.Second) // the backstop, well past the timeouts
	defer cancel()
	start := time.Now()
	err := uow.Do(wctx, func(ctx context.Context) error { return lock.Lock(ctx) })
	elapsed := time.Since(start)
	close(release)
	if herr := <-held; herr != nil {
		t.Fatalf("holder: %v", herr)
	}
	assertTimedOut(t, "second unit's flow lock", err, elapsed)
	for _, setting := range []string{"statement_timeout", "lock_timeout"} {
		var v string
		if err := pool.QueryRow(ctx, "SHOW "+setting).Scan(&v); err != nil || v != "0" {
			t.Fatalf("%s outside a unit = %q (%v): the unit's SET must be LOCAL, never leak to the session", setting, v, err)
		}
	}
}

// carry (Task 13 review): the relay's pass (Drain) and a stand-alone Insert (inTx) begin their own
// transactions, so they state the same SET LOCAL timeouts. An ACCESS EXCLUSIVE lock on
// outbox_events makes both wait; on a pool with no session settings only their own lock_timeout
// ends the wait.
func TestDrainAndAnInsertOutsideAUnitCarryTheTimeouts(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.BarePool(t)
	repo := postgres.NewOutboxRepo(pool)
	holder, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Rollback(ctx) }()
	if _, err := holder.Exec(ctx, `LOCK TABLE outbox_events IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	for name, run := range map[string]func(context.Context) error{
		"Drain": func(c context.Context) error {
			_, err := repo.Drain(c, 10, 0, func(context.Context, outbox.Message) error { return nil })
			return err
		},
		"Insert outside a unit (inTx)": func(c context.Context) error {
			return repo.Insert(c, outbox.Message{EventID: "e", Topic: "t", EventType: "x", Subject: "s", DataSchema: "u", Value: []byte("{}")})
		},
	} {
		wctx, cancel := context.WithTimeout(ctx, 12*time.Second) // the backstop, well past the timeouts
		start := time.Now()
		err := run(wctx)
		elapsed := time.Since(start)
		cancel()
		assertTimedOut(t, name, err, elapsed)
	}
}

func assertTimedOut(t *testing.T, name string, err error, elapsed time.Duration) {
	t.Helper()
	var pg interface{ SQLState() string } // *pgconn.PgError
	if !errors.As(err, &pg) || (pg.SQLState() != "55P03" && pg.SQLState() != "57014") || elapsed > 6500*time.Millisecond {
		t.Errorf("%s = %v after %v; want SQLSTATE 55P03 (lock_timeout) or 57014 (statement_timeout) within ~6 s", name, err, elapsed)
	}
}
