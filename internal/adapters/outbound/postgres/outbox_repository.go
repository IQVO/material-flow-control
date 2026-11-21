package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/IQVO/material-flow-control/internal/application/outbox"
)

// OutboxRepo is the transactional outbox: Insert runs in the caller's unit of work, Drain is
// the relay's pass.
type OutboxRepo struct{ pool *pgxpool.Pool }

func NewOutboxRepo(pool *pgxpool.Pool) *OutboxRepo { return &OutboxRepo{pool: pool} }

type headerJSON struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

func marshalHeaders(hs []outbox.Header) ([]byte, error) {
	out := make([]headerJSON, len(hs))
	for i, h := range hs {
		out[i] = headerJSON{Key: h.Key, Value: h.Value}
	}
	return json.Marshal(out)
}

func unmarshalHeaders(b []byte) ([]outbox.Header, error) {
	var in []headerJSON
	if err := json.Unmarshal(b, &in); err != nil {
		return nil, err
	}
	out := make([]outbox.Header, len(in))
	for i, h := range in {
		out[i] = outbox.Header{Key: h.Key, Value: h.Value}
	}
	return out, nil
}

// Insert stores msgs as unpublished rows, on the unit of work's transaction when ctx has one.
func (r *OutboxRepo) Insert(ctx context.Context, msgs ...outbox.Message) error {
	if len(msgs) == 0 {
		return nil
	}
	return inTx(ctx, r.pool, func(q querier) error {
		for _, m := range msgs {
			headers, err := marshalHeaders(m.Headers)
			if err != nil {
				return fmt.Errorf("marshal outbox headers for %s: %w", m.EventType, err)
			}
			if _, err := q.Exec(ctx, `INSERT INTO outbox_events (event_id, topic, event_type, subject, key, dataschema, value, headers)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, m.EventID, m.Topic, m.EventType, m.Subject, m.Key, m.DataSchema, m.Value, headers); err != nil {
				return fmt.Errorf("enqueue outbox event %s: %w", m.EventType, err)
			}
		}
		return nil
	})
}

// Backlog is the unpublished outbox (the material_flow_control.outbox.backlog and .lag gauges):
// how many rows wait, attempt-capped ones included, and how long the oldest of them has waited
// on the database's clock (0 when nothing waits).
func (r *OutboxRepo) Backlog(ctx context.Context) (rows int64, oldest time.Duration, err error) {
	var seconds float64
	err = r.pool.QueryRow(ctx, `SELECT count(*), COALESCE(EXTRACT(EPOCH FROM now() - min(created_at)), 0)::float8
		FROM outbox_events WHERE published_at IS NULL`).Scan(&rows, &seconds)
	if err != nil {
		return 0, 0, fmt.Errorf("read the outbox backlog: %w", err)
	}
	return rows, time.Duration(seconds * float64(time.Second)), nil
}

type pendingRow struct {
	id  int64
	msg outbox.Message
}

// Drain claims up to limit unpublished rows (FOR UPDATE SKIP LOCKED), sends them one at a time
// in id order and stops at the first failure, which it records on the row (at-least-once,
// same CloudEvents id on a retry). maxAttempts > 0 skips rows that reached the cap.
func (r *OutboxRepo) Drain(ctx context.Context, limit, maxAttempts int, send func(context.Context, outbox.Message) error) (int, error) {
	tx, err := begin(ctx, r.pool) // carry (Task 13 review): SET LOCAL timeouts
	if err != nil {
		return 0, fmt.Errorf("begin outbox pass: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	batch, err := claim(ctx, tx, limit, maxAttempts)
	if err != nil {
		return 0, err
	}
	published := 0
	for _, row := range batch {
		if err := send(ctx, row.msg); err != nil {
			return published, recordFailure(ctx, tx, row, err)
		}
		if _, err := tx.Exec(ctx, `UPDATE outbox_events SET published_at = now(), attempts = attempts + 1, last_error = NULL WHERE id = $1`, row.id); err != nil {
			return published, fmt.Errorf("mark outbox row %d published: %w", row.id, err)
		}
		published++
	}
	if err := tx.Commit(ctx); err != nil {
		return published, fmt.Errorf("commit outbox pass: %w", err)
	}
	return published, nil
}

func recordFailure(ctx context.Context, tx pgx.Tx, row pendingRow, sendErr error) error {
	dctx := context.WithoutCancel(ctx)
	failure := &outbox.SendFailureError{RowID: row.id, Message: row.msg, Err: sendErr}
	if uerr := tx.QueryRow(dctx, `UPDATE outbox_events SET attempts = attempts + 1, last_error = $2 WHERE id = $1 RETURNING attempts`,
		row.id, sendErr.Error()).Scan(&failure.Attempts); uerr != nil {
		failure.Err = errors.Join(failure.Err, fmt.Errorf("record outbox failure: %w", uerr))
	}
	if cerr := tx.Commit(dctx); cerr != nil {
		failure.Attempts = 0
		failure.Err = errors.Join(failure.Err, fmt.Errorf("commit outbox pass: %w", cerr))
	}
	return failure
}

func claim(ctx context.Context, tx pgx.Tx, limit, maxAttempts int) ([]pendingRow, error) {
	capFilter, args := "", []any{limit}
	if maxAttempts > 0 {
		capFilter, args = "AND attempts < $2", append(args, maxAttempts)
	}
	rows, err := tx.Query(ctx, `SELECT id, event_id, topic, event_type, subject, key, dataschema, value, headers
		FROM outbox_events WHERE published_at IS NULL `+capFilter+` ORDER BY id LIMIT $1 FOR UPDATE SKIP LOCKED`, args...)
	if err != nil {
		return nil, fmt.Errorf("claim outbox rows: %w", err)
	}
	defer rows.Close()
	var batch []pendingRow
	for rows.Next() {
		var p pendingRow
		var headers []byte
		if err := rows.Scan(&p.id, &p.msg.EventID, &p.msg.Topic, &p.msg.EventType, &p.msg.Subject, &p.msg.Key, &p.msg.DataSchema, &p.msg.Value, &headers); err != nil {
			return nil, fmt.Errorf("scan outbox row: %w", err)
		}
		if p.msg.Headers, err = unmarshalHeaders(headers); err != nil {
			return nil, fmt.Errorf("decode outbox row %d headers: %w", p.id, err)
		}
		batch = append(batch, p)
	}
	return batch, rows.Err()
}
