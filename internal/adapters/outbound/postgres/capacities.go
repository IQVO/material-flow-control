package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/IQVO/material-flow-control/internal/application/facts"
	"github.com/IQVO/material-flow-control/internal/application/ports"
	"github.com/IQVO/material-flow-control/internal/domain/capacity"
)

// Capacities is the Postgres ports.Capacities.
type Capacities struct{ pool *pgxpool.Pool }

func NewCapacities(pool *pgxpool.Pool) *Capacities { return &Capacities{pool: pool} }

var _ ports.Capacities = (*Capacities)(nil)

func (r *Capacities) Get(ctx context.Context, kind capacity.Kind, id string) (capacity.Snapshot, bool, error) {
	var b []byte
	err := queryFor(ctx, r.pool).QueryRow(ctx, `SELECT snapshot FROM subsystem_capacities WHERE kind = $1 AND subsystem_id = $2`,
		string(kind), id).Scan(&b)
	if errors.Is(err, pgx.ErrNoRows) {
		return capacity.Snapshot{}, false, nil
	}
	if err != nil {
		return capacity.Snapshot{}, false, fmt.Errorf("get capacity: %w", err)
	}
	s, err := decode[capacity.Snapshot](b)
	return s, err == nil, err
}

func (r *Capacities) Save(ctx context.Context, s capacity.Snapshot) error {
	b, err := encode(s)
	if err != nil {
		return err
	}
	_, err = queryFor(ctx, r.pool).Exec(ctx, `INSERT INTO subsystem_capacities (kind, subsystem_id, snapshot, measured_at) VALUES ($1, $2, $3, $4)
		ON CONFLICT (kind, subsystem_id) DO UPDATE SET snapshot = EXCLUDED.snapshot, measured_at = EXCLUDED.measured_at`,
		string(s.Kind), s.ID, b, s.MeasuredAt.UTC())
	if err != nil {
		return fmt.Errorf("save capacity: %w", err)
	}
	return nil
}

func (r *Capacities) List(ctx context.Context) ([]capacity.Snapshot, error) {
	rows, err := queryFor(ctx, r.pool).Query(ctx, `SELECT snapshot FROM subsystem_capacities ORDER BY kind, subsystem_id`)
	if err != nil {
		return nil, fmt.Errorf("list capacities: %w", err)
	}
	defer rows.Close()
	var out []capacity.Snapshot
	for rows.Next() {
		var b []byte
		if err := rows.Scan(&b); err != nil {
			return nil, err
		}
		s, err := decode[capacity.Snapshot](b)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// Units is the Postgres ports.EquipmentUnits.
type Units struct{ pool *pgxpool.Pool }

func NewUnits(pool *pgxpool.Pool) *Units { return &Units{pool: pool} }

var _ ports.EquipmentUnits = (*Units)(nil)

func (r *Units) Save(ctx context.Context, u facts.EquipmentState) error {
	b, err := encode(u)
	if err != nil {
		return err
	}
	_, err = queryFor(ctx, r.pool).Exec(ctx, `INSERT INTO equipment_units (asrs_id, unit_id, state) VALUES ($1, $2, $3)
		ON CONFLICT (asrs_id, unit_id) DO UPDATE SET state = EXCLUDED.state`, u.AsrsID, u.UnitID, b)
	if err != nil {
		return fmt.Errorf("save equipment unit: %w", err)
	}
	return nil
}

func (r *Units) List(ctx context.Context, asrsID string) ([]facts.EquipmentState, error) {
	rows, err := queryFor(ctx, r.pool).Query(ctx, `SELECT state FROM equipment_units WHERE asrs_id = $1 ORDER BY unit_id`, asrsID)
	if err != nil {
		return nil, fmt.Errorf("list equipment units: %w", err)
	}
	defer rows.Close()
	var out []facts.EquipmentState
	for rows.Next() {
		var b []byte
		if err := rows.Scan(&b); err != nil {
			return nil, err
		}
		u, err := decode[facts.EquipmentState](b)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}
