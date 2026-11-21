package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/IQVO/material-flow-control/internal/application/ports"
	"github.com/IQVO/material-flow-control/internal/domain/transportrequest"
)

// Requests is the Postgres ports.TransportRequests.
type Requests struct{ pool *pgxpool.Pool }

func NewRequests(pool *pgxpool.Pool) *Requests { return &Requests{pool: pool} }

var _ ports.TransportRequests = (*Requests)(nil)

func (r *Requests) Get(ctx context.Context, id string) (transportrequest.Record, error) {
	var b []byte
	err := queryFor(ctx, r.pool).QueryRow(ctx, `SELECT record FROM transport_requests WHERE transport_request_id = $1`, id).Scan(&b)
	if errors.Is(err, pgx.ErrNoRows) {
		return transportrequest.Record{}, ports.ErrNotFound
	}
	if err != nil {
		return transportrequest.Record{}, fmt.Errorf("get transport request: %w", err)
	}
	return decode[transportrequest.Record](b)
}

func (r *Requests) Insert(ctx context.Context, rec transportrequest.Record) error {
	b, err := encode(rec)
	if err != nil {
		return err
	}
	tag, err := queryFor(ctx, r.pool).Exec(ctx, `INSERT INTO transport_requests
		(transport_request_id, container_id, outcome, transport_order_id, record, recorded_at)
		VALUES ($1, $2, $3, NULLIF($4, ''), $5, $6) ON CONFLICT (transport_request_id) DO NOTHING`,
		rec.Request.ID, rec.Request.ContainerID, string(rec.Outcome), rec.OrderID, b, rec.At.UTC())
	if err != nil {
		return fmt.Errorf("insert transport request: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ports.ErrDuplicate
	}
	return nil
}
