package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/IQVO/material-flow-control/internal/application/ports"
	"github.com/IQVO/material-flow-control/internal/domain/stationqueue"
)

// Stations is the Postgres ports.StationQueues.
type Stations struct{ pool *pgxpool.Pool }

func NewStations(pool *pgxpool.Pool) *Stations { return &Stations{pool: pool} }

var _ ports.StationQueues = (*Stations)(nil)

func (r *Stations) Get(ctx context.Context, id string) (*stationqueue.Queue, error) {
	var b []byte
	err := queryFor(ctx, r.pool).QueryRow(ctx, `SELECT snapshot FROM station_queues WHERE station_id = $1`, id).Scan(&b)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ports.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get station queue: %w", err)
	}
	s, err := decode[stationqueue.Snapshot](b)
	if err != nil {
		return nil, err
	}
	return stationqueue.Restore(s), nil
}

func (r *Stations) Save(ctx context.Context, q *stationqueue.Queue) error {
	s := q.Snapshot()
	s.Version++
	b, err := encode(s)
	if err != nil {
		return err
	}
	_, err = queryFor(ctx, r.pool).Exec(ctx, `INSERT INTO station_queues (station_id, snapshot, version) VALUES ($1, $2, $3)
		ON CONFLICT (station_id) DO UPDATE SET snapshot = EXCLUDED.snapshot, version = EXCLUDED.version`, s.StationID, b, s.Version)
	if err != nil {
		return fmt.Errorf("save station queue: %w", err)
	}
	return nil
}
