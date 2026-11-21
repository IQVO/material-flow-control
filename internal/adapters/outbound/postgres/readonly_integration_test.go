//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/IQVO/material-flow-control/internal/adapters/outbound/postgres"
	"github.com/IQVO/material-flow-control/internal/application/ports"
	ct "github.com/IQVO/material-flow-control/internal/domain/containertrack"
	tr "github.com/IQVO/material-flow-control/internal/domain/transportrequest"
	"github.com/IQVO/material-flow-control/internal/testing/pgtest"
)

// ruling P4-R17: cmd/mcp and cmd/a2a read through postgres.NewReadOnlyPool. PgBouncer's transaction pooling may
// refuse a default_transaction_read_only startup parameter, so the adapter itself begins every transaction on
// that pool READ ONLY: a write attempted through any of its paths (a repository outside a unit of work, inTx, a
// unit of work, a raw statement on the querier) fails with SQLSTATE 25006 and writes nothing.

const readOnlySQLTransaction = "25006"

func readOnlyPools(t *testing.T) (rw, ro *postgres.Pool) {
	t.Helper()
	ctx := context.Background()
	url := pgtest.URL(t)
	rw, err := postgres.NewPool(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(rw.Close)
	ro, err = postgres.NewReadOnlyPool(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeWithin(t, ro, 5*time.Second) })
	return rw, ro
}

// closeWithin closes pool, failing instead of hanging when a connection is never handed back (Close waits for
// every acquired connection).
func closeWithin(t *testing.T, pool *postgres.Pool, d time.Duration) {
	t.Helper()
	done := make(chan struct{})
	go func() { pool.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(d):
		t.Errorf("closing the read-only pool hung for %s with %d connections acquired: a transaction never ended", d, pool.Stat().AcquiredConns())
	}
}

func assertReadOnlyRefusal(t *testing.T, what string, err error) {
	t.Helper()
	var pgErr interface{ SQLState() string } // *pgconn.PgError, named without importing the driver
	if !errors.As(err, &pgErr) || pgErr.SQLState() != readOnlySQLTransaction {
		t.Errorf("%s through the read-only pool = %v, want SQLSTATE %s (read_only_sql_transaction)", what, err, readOnlySQLTransaction)
	}
}

func otherRequest() tr.Request {
	r := request()
	r.ID, r.ContainerID = "rq-2", "TOTE-2"
	return r
}

func TestAWriteThroughTheReadOnlyPoolFailsWithSQLState25006(t *testing.T) {
	ctx := context.Background()
	rw, ro := readOnlyPools(t)
	if err := postgres.NewRequests(rw).Insert(ctx, tr.Accept(request(), "to-1", t0)); err != nil {
		t.Fatal(err)
	}
	if got, err := postgres.NewRequests(ro).Get(ctx, "rq-1"); err != nil || got.OrderID != "to-1" {
		t.Fatalf("read through the read-only pool = %+v %v", got, err)
	}
	if _, err := postgres.NewRequests(ro).Get(ctx, "nope"); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("unknown id through the read-only pool = %v, want ErrNotFound", err)
	}
	rec := tr.Accept(otherRequest(), "to-2", t0)
	assertReadOnlyRefusal(t, "Requests.Insert outside a unit (queryFor Exec)", postgres.NewRequests(ro).Insert(ctx, rec))
	assertReadOnlyRefusal(t, "Tracks.Save outside a unit (inTx)", postgres.NewTracks(ro).Save(ctx, ct.New("TOTE-2", t0)))
	assertReadOnlyRefusal(t, "a unit of work", postgres.NewUnitOfWork(ro).Do(ctx, func(ctx context.Context) error {
		return postgres.NewRequests(ro).Insert(ctx, rec)
	}))
	q := postgres.QuerierFor(ctx, ro)
	_, err := q.Exec(ctx, `DELETE FROM transport_requests`)
	assertReadOnlyRefusal(t, "a raw Exec", err)
	var n int
	assertReadOnlyRefusal(t, "a raw QueryRow", q.QueryRow(ctx, `UPDATE flow_lock SET id = id RETURNING id`).Scan(&n))
	rows, err := q.Query(ctx, `DELETE FROM transport_requests RETURNING transport_request_id`)
	if err == nil {
		for rows.Next() {
		}
		rows.Close()
		err = rows.Err()
	}
	assertReadOnlyRefusal(t, "a raw Query", err)

	if _, err := postgres.NewRequests(rw).Get(ctx, "rq-1"); err != nil {
		t.Fatalf("rq-1 after the refused writes = %v: a refused write deleted it", err)
	}
	if _, err := postgres.NewRequests(rw).Get(ctx, "rq-2"); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("rq-2 after the refused writes = %v, want ErrNotFound", err)
	}
	if _, err := postgres.NewTracks(rw).Get(ctx, "TOTE-2"); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("TOTE-2 after the refused writes = %v, want ErrNotFound", err)
	}
	// cmd/api's pool stays read-write.
	if err := postgres.NewRequests(rw).Insert(ctx, rec); err != nil {
		t.Fatalf("the read-write pool refused a write: %v", err)
	}
}

// Every statement on the read-only pool runs inside a READ ONLY transaction, so transaction_read_only reads "on"
// whatever the session default says; the read-write pool's transactions read "off".
func TestTheReadOnlyPoolBeginsEveryTransactionReadOnly(t *testing.T) {
	ctx := context.Background()
	rw, ro := readOnlyPools(t)
	for _, c := range []struct {
		name string
		pool *postgres.Pool
		want string
	}{{"read-only", ro, "on"}, {"read-write", rw, "off"}} {
		var v string
		if err := postgres.QuerierFor(ctx, c.pool).QueryRow(ctx, `SHOW transaction_read_only`).Scan(&v); err != nil || v != c.want {
			t.Errorf("%s pool outside a unit: transaction_read_only = %q (%v), want %q", c.name, v, err, c.want)
		}
		err := postgres.NewUnitOfWork(c.pool).Do(ctx, func(ctx context.Context) error {
			return postgres.QuerierFor(ctx, c.pool).QueryRow(ctx, `SHOW transaction_read_only`).Scan(&v)
		})
		if err != nil || v != c.want {
			t.Errorf("%s pool in a unit: transaction_read_only = %q (%v), want %q", c.name, v, err, c.want)
		}
	}
}

// A list read on the read-only pool holds its transaction until the rows close; closing them must end it and
// hand the connection back, or the agent runs out of its MaxConns connections after that many reads.
func TestReadOnlyListReadsReleaseTheirConnections(t *testing.T) {
	ctx := context.Background()
	rw, ro := readOnlyPools(t)
	if err := postgres.NewTracks(rw).Save(ctx, ct.New("TOTE-1", t0)); err != nil {
		t.Fatal(err)
	}
	orders, tracks := postgres.NewOrders(ro), postgres.NewTracks(ro)
	for i := 0; i < 3*postgres.MaxConns; i++ {
		rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		_, oerr := orders.Open(rctx)
		_, herr := tracks.History(rctx, "TOTE-1", 20)
		cancel()
		if oerr != nil || herr != nil {
			t.Fatalf("list read %d on the read-only pool: Open=%v History=%v (stat: %+v)", i, oerr, herr, ro.Stat())
		}
	}
	if acquired := ro.Stat().AcquiredConns(); acquired != 0 {
		t.Fatalf("%d connections still acquired after the reads: a rows' transaction never ended", acquired)
	}
	if err := postgres.NewTracks(ro).Save(ctx, ct.New("TOTE-9", t0)); err == nil {
		t.Fatal("a write succeeded on the read-only pool after the list reads")
	}
}

// carry (Task 23 review): a Query that fails before any row (a statement the server refuses at prepare) must end
// its READ ONLY transaction, or each failure pins one connection until the pool is exhausted.
func TestReadOnlyQueryErrorsReleaseTheirConnections(t *testing.T) {
	ctx := context.Background()
	_, ro := readOnlyPools(t)
	q := postgres.QuerierFor(ctx, ro)
	for i := 0; i < 3*postgres.MaxConns; i++ {
		rctx, cancel := context.WithTimeout(ctx, 1*time.Second)
		rows, err := q.Query(rctx, `SELEC 1`)
		cancel()
		if err == nil {
			rows.Close()
			t.Fatalf("query %d: a malformed statement was accepted", i)
		}
	}
	if acquired := ro.Stat().AcquiredConns(); acquired != 0 {
		t.Fatalf("%d connections still acquired after %d failed queries: the transaction of a failed Query never ended", acquired, 3*postgres.MaxConns)
	}
}
