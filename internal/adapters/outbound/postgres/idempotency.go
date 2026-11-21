package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/IQVO/material-flow-control/internal/application/idempotency"
	"github.com/IQVO/material-flow-control/internal/application/ports"
)

// IdempotencyStore is the pgx ports.IdempotencyStore (24-hour window enforced by notBefore), as asrs-control's.
type IdempotencyStore struct{ pool *pgxpool.Pool }

func NewIdempotencyStore(pool *pgxpool.Pool) *IdempotencyStore { return &IdempotencyStore{pool: pool} }

var _ ports.IdempotencyStore = (*IdempotencyStore)(nil)

func (s *IdempotencyStore) Lookup(ctx context.Context, key string, notBefore time.Time) (idempotency.IdempotencyRecord, bool, error) {
	rec := idempotency.IdempotencyRecord{Key: key}
	err := queryFor(ctx, s.pool).QueryRow(ctx, `SELECT fingerprint, status, content_type, body, created_at
		FROM idempotency_keys WHERE key = $1 AND created_at >= $2`, key, notBefore.UTC()).
		Scan(&rec.Fingerprint, &rec.Status, &rec.ContentType, &rec.Body, &rec.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return idempotency.IdempotencyRecord{}, false, nil
	}
	if err != nil {
		return idempotency.IdempotencyRecord{}, false, fmt.Errorf("lookup idempotency key: %w", err)
	}
	rec.CreatedAt = rec.CreatedAt.UTC()
	return rec, true, nil
}

// Save keeps the first writer of a key; a row older than notBefore is replaced.
func (s *IdempotencyStore) Save(ctx context.Context, rec idempotency.IdempotencyRecord, notBefore time.Time) error {
	body := rec.Body
	if body == nil {
		body = []byte{}
	}
	_, err := queryFor(ctx, s.pool).Exec(ctx, `INSERT INTO idempotency_keys (key, fingerprint, status, content_type, body, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (key) DO UPDATE SET fingerprint = EXCLUDED.fingerprint, status = EXCLUDED.status,
			content_type = EXCLUDED.content_type, body = EXCLUDED.body, created_at = EXCLUDED.created_at
		WHERE idempotency_keys.created_at < $7`,
		rec.Key, rec.Fingerprint, rec.Status, rec.ContentType, body, rec.CreatedAt.UTC(), notBefore.UTC())
	if err != nil {
		return fmt.Errorf("save idempotency key: %w", err)
	}
	return nil
}

// Purge deletes answers older than before. carry (Task 19 review): through inTx, so its own SET LOCAL
// timeouts bound it under PgBouncer transaction pooling.
func (s *IdempotencyStore) Purge(ctx context.Context, before time.Time) (int64, error) {
	var n int64
	err := inTx(ctx, s.pool, func(q querier) error {
		tag, err := q.Exec(ctx, `DELETE FROM idempotency_keys WHERE created_at < $1`, before.UTC())
		n = tag.RowsAffected()
		return err
	})
	return n, err
}
