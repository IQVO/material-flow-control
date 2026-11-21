package postgres

import (
	"context"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ruling P4-R17: cmd/mcp and cmd/a2a read through a READ-ONLY pool. PgBouncer's transaction pooling can refuse a
// default_transaction_read_only startup parameter (and a session SET may sit on another server connection), so
// read-only is enforced here, per transaction: every transaction this adapter begins on such a pool is BEGIN
// READ ONLY (begin), and a repository read outside a unit of work runs in one of its own (readOnlyQuerier)
// instead of an autocommit statement. A write through any path fails with SQLSTATE 25006. cmd/api keeps NewPool.

// readOnlyPools holds every pool NewReadOnlyPool opened. One per agent process; an entry is never removed (a
// closed pool begins nothing).
var readOnlyPools sync.Map

// NewReadOnlyPool opens a pool like NewPool whose every transaction this adapter begins is READ ONLY.
func NewReadOnlyPool(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	pool, err := NewPool(ctx, databaseURL)
	if err != nil {
		return nil, err
	}
	readOnlyPools.Store(pool, struct{}{})
	return pool, nil
}

func isReadOnly(pool *pgxpool.Pool) bool {
	_, ok := readOnlyPools.Load(pool)
	return ok
}

// txOptions is READ COMMITTED (stated, not inherited), READ ONLY on a NewReadOnlyPool pool.
func txOptions(pool *pgxpool.Pool) pgx.TxOptions {
	o := pgx.TxOptions{IsoLevel: pgx.ReadCommitted}
	if isReadOnly(pool) {
		o.AccessMode = pgx.ReadOnly
	}
	return o
}

// readOnlyQuerier runs each statement outside a unit of work in a READ ONLY transaction of its own.
type readOnlyQuerier struct{ pool *pgxpool.Pool }

func (q readOnlyQuerier) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	var tag pgconn.CommandTag
	err := inTx(ctx, q.pool, func(tx querier) error {
		var err error
		tag, err = tx.Exec(ctx, sql, args...)
		return err
	})
	return tag, err
}

func (q readOnlyQuerier) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return readOnlyRow{ctx: ctx, pool: q.pool, sql: sql, args: args}
}

// Query keeps its transaction open until the rows close (txRows).
func (q readOnlyQuerier) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	tx, err := begin(ctx, q.pool)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, sql, args...)
	if err != nil {
		_ = tx.Rollback(context.WithoutCancel(ctx))
		return nil, err
	}
	return &txRows{Rows: rows, ctx: ctx, tx: tx}, nil
}

// readOnlyRow defers the statement to Scan, which runs it in its own READ ONLY transaction; the statement's error
// (pgx.ErrNoRows included) comes back unwrapped.
type readOnlyRow struct {
	ctx  context.Context
	pool *pgxpool.Pool
	sql  string
	args []any
}

func (r readOnlyRow) Scan(dest ...any) error {
	return inTx(r.ctx, r.pool, func(tx querier) error { return tx.QueryRow(r.ctx, r.sql, r.args...).Scan(dest...) })
}

// txRows ends its READ ONLY transaction when the rows close (a read-only transaction has nothing to commit) and
// so hands the connection back to the pool.
type txRows struct {
	pgx.Rows
	ctx  context.Context
	tx   pgx.Tx
	once sync.Once
}

func (r *txRows) Close() {
	r.Rows.Close()
	r.once.Do(func() { _ = r.tx.Rollback(context.WithoutCancel(r.ctx)) })
}
