package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/IQVO/material-flow-control/internal/adapters/outbound/postgres/pgtx"
)

// querier is the subset of pgx shared by *pgxpool.Pool and pgx.Tx the repositories need.
type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type hooksKey struct{}

// hooks are the after-commit functions of the outermost unit of work.
type hooks struct{ fns []func() }

// UnitOfWork is the pgx ports.UnitOfWork: Do begins one READ COMMITTED transaction carried in
// ctx (pgtx), commits on a nil return and rolls back otherwise; a ctx that already carries a
// transaction is joined. After a successful outermost commit the AfterCommit hooks run in
// order; a rollback or a panic drops them.
type UnitOfWork struct{ pool *pgxpool.Pool }

func NewUnitOfWork(pool *pgxpool.Pool) *UnitOfWork { return &UnitOfWork{pool: pool} }

func (u *UnitOfWork) Do(ctx context.Context, fn func(ctx context.Context) error) error {
	if _, ok := pgtx.From(ctx); ok {
		return fn(ctx)
	}
	// READ COMMITTED is stated, not inherited: the flow lock row (SELECT ... FOR UPDATE, decision
	// D19) serialises writers; no snapshot isolation is wanted.
	tx, err := begin(ctx, u.pool)
	if err != nil {
		return fmt.Errorf("begin unit of work: %w", err)
	}
	// Rollback after Commit is a harmless no-op; it also covers a panic in fn.
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	h := &hooks{}
	if err := fn(context.WithValue(pgtx.With(ctx, tx), hooksKey{}, h)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit unit of work: %w", err)
	}
	for _, f := range h.fns {
		f()
	}
	return nil
}

// AfterCommit implements ports.UnitOfWork.
func (u *UnitOfWork) AfterCommit(ctx context.Context, fn func()) {
	if h, ok := ctx.Value(hooksKey{}).(*hooks); ok {
		h.fns = append(h.fns, fn)
		return
	}
	fn()
}

// queryFor returns the transaction carried by ctx, or pool (a READ ONLY transaction per statement on a
// NewReadOnlyPool pool, ruling P4-R17).
func queryFor(ctx context.Context, pool *pgxpool.Pool) querier {
	if tx, ok := pgtx.From(ctx); ok {
		return tx
	}
	if isReadOnly(pool) {
		return readOnlyQuerier{pool: pool}
	}
	return pool
}

// inTx runs fn on the ctx transaction, or on a transaction of its own.
func inTx(ctx context.Context, pool *pgxpool.Pool, fn func(q querier) error) error {
	if tx, ok := pgtx.From(ctx); ok {
		return fn(tx)
	}
	tx, err := begin(ctx, pool)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// LockTimeout bounds how long a statement waits for a row lock (the flow lock, decision D19).
const LockTimeout = "5s"

// carry (Task 13 review): PgBouncer runs pool_mode=transaction, so a session-level SET (NewPool's
// AfterConnect) may sit on a server connection this transaction never runs on. Every transaction
// therefore states its own timeouts with SET LOCAL: they end with it and never leak to the next
// client of the server connection.
const setLocalTimeouts = "SET LOCAL statement_timeout = '" + StatementTimeout + "'; SET LOCAL lock_timeout = '" + LockTimeout + "'"

// begin starts a READ COMMITTED transaction with the timeouts set LOCAL to it: the only way this
// adapter opens a transaction (UnitOfWork.Do, inTx, the relay's Drain, readOnlyQuerier). ruling P4-R17: on a
// NewReadOnlyPool pool it is BEGIN READ ONLY (txOptions).
func begin(ctx context.Context, pool *pgxpool.Pool) (pgx.Tx, error) {
	tx, err := pool.BeginTx(ctx, txOptions(pool))
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, setLocalTimeouts); err != nil {
		_ = tx.Rollback(context.WithoutCancel(ctx))
		return nil, fmt.Errorf("set transaction timeouts: %w", err)
	}
	return tx, nil
}
