package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/IQVO/material-flow-control/internal/adapters/outbound/postgres/pgtx"
	"github.com/IQVO/material-flow-control/internal/application/ports"
)

// FlowLock is the Postgres ports.FlowLock: a row lock on flow_lock held until the unit of work ends (decision D19).
type FlowLock struct{ pool *pgxpool.Pool }

func NewFlowLock(pool *pgxpool.Pool) *FlowLock { return &FlowLock{pool: pool} }

var _ ports.FlowLock = (*FlowLock)(nil)

// Lock needs the unit of work's transaction in ctx: outside one a row lock would end at once.
func (l *FlowLock) Lock(ctx context.Context) error {
	tx, ok := pgtx.From(ctx)
	if !ok {
		return errors.New("the flow lock needs a unit of work")
	}
	var id int
	if err := tx.QueryRow(ctx, `SELECT id FROM flow_lock WHERE id = 1 FOR UPDATE`).Scan(&id); err != nil {
		return fmt.Errorf("take the flow lock: %w", err)
	}
	return nil
}
