package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Retention is one table's retention policy (ADR 0008, issue #3): Purge deletes the policy's terminal rows older
// than a cutoff in batches of at most batch rows, one transaction per batch (its own SET LOCAL timeouts bound every
// batch under PgBouncer transaction pooling, as the idempotency and inbox purges), until a batch comes back short.
// A policy never deletes a row a live order or a live aggregate still references.
type Retention struct {
	pool  *pgxpool.Pool
	table string
	batch int
	// flowLock: the batch takes the flow lock first (decision D19), so it never interleaves with a unit of work.
	flowLock bool
	// sql deletes one batch: $1 is the cutoff, $2 the batch size, then extra.
	sql   string
	extra []any
}

// Table is the table the policy purges, the `table` attribute of material_flow_control.retention.purged.
func (r *Retention) Table() string { return r.table }

// Purge deletes every row of the policy older than before, batch after batch, and returns how many it deleted.
func (r *Retention) Purge(ctx context.Context, before time.Time) (int64, error) {
	var total int64
	for {
		n, err := r.purgeBatch(ctx, before)
		total += n
		if err != nil {
			return total, fmt.Errorf("purge %s: %w", r.table, err)
		}
		if n < int64(r.batch) {
			return total, nil
		}
		if err := ctx.Err(); err != nil {
			return total, fmt.Errorf("purge %s: %w", r.table, err)
		}
	}
}

func (r *Retention) purgeBatch(ctx context.Context, before time.Time) (int64, error) {
	var n int64
	err := inTx(ctx, r.pool, func(q querier) error {
		if r.flowLock {
			if _, err := q.Exec(ctx, `SELECT id FROM flow_lock WHERE id = 1 FOR UPDATE`); err != nil {
				return fmt.Errorf("take the flow lock: %w", err)
			}
		}
		tag, err := q.Exec(ctx, r.sql, append([]any{before.UTC(), r.batch}, r.extra...)...)
		n = tag.RowsAffected()
		return err
	})
	if err != nil {
		return 0, err
	}
	return n, nil
}

func newRetention(pool *pgxpool.Pool, table string, batch int, sql string, extra ...any) *Retention {
	return &Retention{pool: pool, table: table, batch: max(batch, 1), sql: sql, extra: extra}
}

// orderRetentionSQL selects ENDED orders last written before the cutoff that nothing live references: no PENDING
// command (a cancellation still to send), no container track holding it active or in clarification, no station
// queue occupant or waiting entry (an occupant keeps its slot after its order ended, until the container leaves).
// The order's request record goes with it (one statement, so no request record ever names a missing order); its
// legs go by ON DELETE CASCADE. JSON paths are the domain snapshots' exported field names (json.go).
const orderRetentionSQL = `WITH victims AS MATERIALIZED (
	SELECT o.transport_order_id FROM transport_orders o
	WHERE o.status IN ('COMPLETED', 'FAILED') AND o.updated_at < $1
	AND NOT EXISTS (SELECT 1 FROM subsystem_commands c WHERE c.transport_order_id = o.transport_order_id AND c.status = 'PENDING')
	AND NOT EXISTS (SELECT 1 FROM container_tracks t WHERE t.active_order_id = o.transport_order_id)
	AND NOT EXISTS (SELECT 1 FROM container_tracks t WHERE t.in_clarification
		AND t.snapshot -> 'Clarification' ->> 'OrderID' = o.transport_order_id)
	AND NOT EXISTS (SELECT 1 FROM station_queues q
		WHERE q.snapshot @> jsonb_build_object('Occupants', jsonb_build_array(jsonb_build_object('OrderID', o.transport_order_id)))
		OR q.snapshot @> jsonb_build_object('Waiting', jsonb_build_array(jsonb_build_object('OrderID', o.transport_order_id))))
	LIMIT $2
), requests AS (
	DELETE FROM transport_requests r USING victims v WHERE r.transport_order_id = v.transport_order_id
)
DELETE FROM transport_orders o USING victims v WHERE o.transport_order_id = v.transport_order_id`

// NewOrderRetention purges ended transport orders with their legs and their ACCEPTED request records, under the
// flow lock: a RequestTransport replay or a late fact reads an order and then expects it to be there.
func NewOrderRetention(pool *pgxpool.Pool, batch int) *Retention {
	r := newRetention(pool, "transport_orders", batch, orderRetentionSQL)
	r.flowLock = true
	return r
}

// NewRejectedRequestRetention purges REJECTED request records (they have no order) recorded before the cutoff.
func NewRejectedRequestRetention(pool *pgxpool.Pool, batch int) *Retention {
	return newRetention(pool, "transport_requests", batch, `DELETE FROM transport_requests WHERE ctid = ANY(ARRAY(
		SELECT ctid FROM transport_requests WHERE outcome = 'REJECTED' AND recorded_at < $1 LIMIT $2))`)
}

// NewContainerMoveRetention purges moves made before the cutoff, but never one of a container's keepLatest newest
// moves (what a track view shows: HistoryLimit) and never a move of a container a live order is moving. A
// keepLatest below 1 keeps 1.
func NewContainerMoveRetention(pool *pgxpool.Pool, batch, keepLatest int) *Retention {
	return newRetention(pool, "container_moves", batch, `DELETE FROM container_moves WHERE ctid = ANY(ARRAY(
		SELECT m.ctid FROM container_moves m WHERE m.moved_at < $1
		AND m.id < (SELECT n.id FROM container_moves n WHERE n.container_id = m.container_id ORDER BY n.id DESC OFFSET $3 LIMIT 1)
		AND NOT EXISTS (SELECT 1 FROM transport_orders o WHERE o.status = 'IN_PROGRESS' AND o.container_id = m.container_id)
		LIMIT $2))`, int64(max(keepLatest, 1)-1))
}

// NewCommandRetention purges DONE and FAILED subsystem commands created before the cutoff whose order is not live
// (a PENDING command is the dispatcher's work and is never purged).
func NewCommandRetention(pool *pgxpool.Pool, batch int) *Retention {
	return newRetention(pool, "subsystem_commands", batch, `DELETE FROM subsystem_commands WHERE ctid = ANY(ARRAY(
		SELECT c.ctid FROM subsystem_commands c WHERE c.status IN ('DONE', 'FAILED') AND c.created_at < $1
		AND NOT EXISTS (SELECT 1 FROM transport_orders o WHERE o.transport_order_id = c.transport_order_id AND o.status = 'IN_PROGRESS')
		LIMIT $2))`)
}

// NewOutboxRetention purges outbox rows PUBLISHED before the cutoff. An unpublished row (attempt-capped ones
// included: the runbook re-arms them) is never purged.
func NewOutboxRetention(pool *pgxpool.Pool, batch int) *Retention {
	return newRetention(pool, "outbox_events", batch, `DELETE FROM outbox_events WHERE ctid = ANY(ARRAY(
		SELECT ctid FROM outbox_events WHERE published_at IS NOT NULL AND published_at < $1 LIMIT $2))`)
}
