package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/IQVO/material-flow-control/internal/application/ports"
)

// ProcessedEvents is the Postgres ports.ProcessedEvents (the inbox; customer-returns cd4ff22 shape).
type ProcessedEvents struct{ pool *pgxpool.Pool }

func NewProcessedEvents(pool *pgxpool.Pool) *ProcessedEvents { return &ProcessedEvents{pool: pool} }

var _ ports.ProcessedEvents = (*ProcessedEvents)(nil)

// Claim records the CloudEvents id and reports whether it was new. Inside a unit of work the INSERT shares the
// transaction of the event's effects, so a rollback un-claims it.
func (r *ProcessedEvents) Claim(ctx context.Context, eventID, eventType string, at time.Time) (bool, error) {
	tag, err := queryFor(ctx, r.pool).Exec(ctx, `INSERT INTO processed_events (event_id, event_type, processed_at) VALUES ($1, $2, $3)
		ON CONFLICT (event_id) DO NOTHING`, eventID, eventType, at.UTC())
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// Purge deletes claims older than before (the hourly purge keeps 7 days: longer than any redelivery window).
func (r *ProcessedEvents) Purge(ctx context.Context, before time.Time) (int64, error) {
	tag, err := r.pool.Exec(ctx, `DELETE FROM processed_events WHERE processed_at < $1`, before.UTC())
	return tag.RowsAffected(), err
}
