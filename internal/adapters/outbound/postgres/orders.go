package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/IQVO/material-flow-control/internal/application/ports"
	"github.com/IQVO/material-flow-control/internal/domain/transportorder"
)

// Orders is the Postgres ports.TransportOrders.
type Orders struct{ pool *pgxpool.Pool }

func NewOrders(pool *pgxpool.Pool) *Orders { return &Orders{pool: pool} }

var _ ports.TransportOrders = (*Orders)(nil)

func (r *Orders) one(ctx context.Context, sql string, arg string) (*transportorder.Order, error) {
	var b []byte
	err := queryFor(ctx, r.pool).QueryRow(ctx, sql, arg).Scan(&b)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ports.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get transport order: %w", err)
	}
	s, err := decode[transportorder.Snapshot](b)
	if err != nil {
		return nil, err
	}
	return transportorder.Restore(s), nil
}

func (r *Orders) Get(ctx context.Context, id string) (*transportorder.Order, error) {
	return r.one(ctx, `SELECT snapshot FROM transport_orders WHERE transport_order_id = $1`, id)
}

func (r *Orders) ByLegRef(ctx context.Context, ref string) (*transportorder.Order, error) {
	return r.one(ctx, `SELECT o.snapshot FROM transport_legs l JOIN transport_orders o USING (transport_order_id) WHERE l.leg_ref = $1`, ref)
}

func (r *Orders) Open(ctx context.Context) ([]*transportorder.Order, error) {
	rows, err := queryFor(ctx, r.pool).Query(ctx, `SELECT snapshot FROM transport_orders WHERE status = 'IN_PROGRESS'
		ORDER BY accepted_at, transport_order_id`)
	if err != nil {
		return nil, fmt.Errorf("list open orders: %w", err)
	}
	defer rows.Close()
	var out []*transportorder.Order
	for rows.Next() {
		var b []byte
		if err := rows.Scan(&b); err != nil {
			return nil, err
		}
		s, err := decode[transportorder.Snapshot](b)
		if err != nil {
			return nil, err
		}
		out = append(out, transportorder.Restore(s))
	}
	return out, rows.Err()
}

func (r *Orders) write(ctx context.Context, o *transportorder.Order, insert bool) error {
	s := o.Snapshot()
	s.Version++
	b, err := encode(s)
	if err != nil {
		return err
	}
	q := queryFor(ctx, r.pool)
	if insert {
		tag, err := q.Exec(ctx, `INSERT INTO transport_orders (transport_order_id, transport_request_id, container_id, status, internal,
			snapshot, version, accepted_at) VALUES ($1, NULLIF($2, ''), $3, $4, $5, $6, $7, $8) ON CONFLICT DO NOTHING`,
			s.ID, s.RequestID, s.ContainerID, string(s.Status), s.Internal, b, s.Version, s.AcceptedAt.UTC())
		if err != nil {
			return fmt.Errorf("insert transport order: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return ports.ErrDuplicate
		}
	} else if _, err := q.Exec(ctx, `UPDATE transport_orders SET status = $2, snapshot = $3, version = $4, updated_at = now()
		WHERE transport_order_id = $1`, s.ID, string(s.Status), b, s.Version); err != nil {
		return fmt.Errorf("save transport order: %w", err)
	}
	refs := make([]string, 0, len(s.Legs))
	for _, l := range s.Legs {
		refs = append(refs, l.Ref)
	}
	if _, err := q.Exec(ctx, `INSERT INTO transport_legs (leg_ref, transport_order_id) SELECT unnest($1::text[]), $2
		ON CONFLICT (leg_ref) DO NOTHING`, refs, s.ID); err != nil {
		return fmt.Errorf("save leg references: %w", err)
	}
	return nil
}

func (r *Orders) Insert(ctx context.Context, o *transportorder.Order) error {
	return r.write(ctx, o, true)
}

func (r *Orders) Save(ctx context.Context, o *transportorder.Order) error {
	return r.write(ctx, o, false)
}
