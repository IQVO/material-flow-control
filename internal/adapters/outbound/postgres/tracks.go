package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/IQVO/material-flow-control/internal/application/ports"
	"github.com/IQVO/material-flow-control/internal/domain/containertrack"
)

// Tracks is the Postgres ports.ContainerTracks.
type Tracks struct{ pool *pgxpool.Pool }

func NewTracks(pool *pgxpool.Pool) *Tracks { return &Tracks{pool: pool} }

var _ ports.ContainerTracks = (*Tracks)(nil)

func (r *Tracks) Get(ctx context.Context, id string) (*containertrack.Track, error) {
	var b []byte
	err := queryFor(ctx, r.pool).QueryRow(ctx, `SELECT snapshot FROM container_tracks WHERE container_id = $1`, id).Scan(&b)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ports.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get container track: %w", err)
	}
	s, err := decode[containertrack.Snapshot](b)
	if err != nil {
		return nil, err
	}
	return containertrack.Restore(s), nil
}

func (r *Tracks) Save(ctx context.Context, t *containertrack.Track) error {
	moves := t.PullMoves()
	s := t.Snapshot()
	s.Version++
	b, err := encode(s)
	if err != nil {
		return err
	}
	return inTx(ctx, r.pool, func(q querier) error {
		if _, err := q.Exec(ctx, `INSERT INTO container_tracks (container_id, position_kind, position_code, active_order_id,
			in_clarification, snapshot, version, updated_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			ON CONFLICT (container_id) DO UPDATE SET position_kind = EXCLUDED.position_kind, position_code = EXCLUDED.position_code,
				active_order_id = EXCLUDED.active_order_id, in_clarification = EXCLUDED.in_clarification,
				snapshot = EXCLUDED.snapshot, version = EXCLUDED.version, updated_at = EXCLUDED.updated_at`,
			s.ContainerID, string(s.Position.Kind), s.Position.Code, s.ActiveOrderID, s.Clarification != nil, b, s.Version, s.UpdatedAt.UTC()); err != nil {
			return fmt.Errorf("save container track: %w", err)
		}
		for _, m := range moves {
			mb, err := encode(m)
			if err != nil {
				return err
			}
			if _, err := q.Exec(ctx, `INSERT INTO container_moves (container_id, move, moved_at) VALUES ($1, $2, $3)`,
				s.ContainerID, mb, m.At.UTC()); err != nil {
				return fmt.Errorf("append container move: %w", err)
			}
		}
		return nil
	})
}

func (r *Tracks) History(ctx context.Context, id string, limit int) ([]containertrack.Move, error) {
	if limit <= 0 { // carry (Task 13 parity): nothing, as the memory double; Postgres refuses a negative LIMIT
		return nil, nil
	}
	rows, err := queryFor(ctx, r.pool).Query(ctx, `SELECT move FROM container_moves WHERE container_id = $1 ORDER BY id DESC LIMIT $2`, id, limit)
	if err != nil {
		return nil, fmt.Errorf("container history: %w", err)
	}
	defer rows.Close()
	var out []containertrack.Move
	for rows.Next() {
		var b []byte
		if err := rows.Scan(&b); err != nil {
			return nil, err
		}
		m, err := decode[containertrack.Move](b)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
