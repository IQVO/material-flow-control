package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/IQVO/material-flow-control/internal/application/command"
	"github.com/IQVO/material-flow-control/internal/application/ports"
)

// Commands is the Postgres ports.Commands (the command outbox).
type Commands struct{ pool *pgxpool.Pool }

func NewCommands(pool *pgxpool.Pool) *Commands { return &Commands{pool: pool} }

var _ ports.Commands = (*Commands)(nil)

func (r *Commands) Enqueue(ctx context.Context, c command.Command) error {
	_, err := queryFor(ctx, r.pool).Exec(ctx, `INSERT INTO subsystem_commands (command_id, transport_order_id, leg_ref, kind,
		idempotency_key, status, attempts, created_at, next_attempt_at, deadline, last_error)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		c.ID, c.OrderID, c.LegRef, string(c.Kind), c.Key, string(c.Status), c.Attempts, c.CreatedAt.UTC(), c.NextAttemptAt.UTC(), c.Deadline.UTC(), c.LastError)
	if err != nil {
		return fmt.Errorf("enqueue subsystem command: %w", err)
	}
	return nil
}

func (r *Commands) Due(ctx context.Context, now time.Time, limit int) ([]command.Command, error) {
	rows, err := queryFor(ctx, r.pool).Query(ctx, `SELECT seq, command_id, transport_order_id, leg_ref, kind, idempotency_key, status,
		attempts, created_at, next_attempt_at, deadline, last_error FROM subsystem_commands
		WHERE status = 'PENDING' AND next_attempt_at <= $1 ORDER BY seq LIMIT $2`, now.UTC(), limit)
	if err != nil {
		return nil, fmt.Errorf("due subsystem commands: %w", err)
	}
	defer rows.Close()
	var out []command.Command
	for rows.Next() {
		var c command.Command
		var kind, status string
		if err := rows.Scan(&c.Seq, &c.ID, &c.OrderID, &c.LegRef, &kind, &c.Key, &status, &c.Attempts,
			&c.CreatedAt, &c.NextAttemptAt, &c.Deadline, &c.LastError); err != nil {
			return nil, err
		}
		c.Kind, c.Status = command.Kind(kind), command.Status(status)
		c.CreatedAt, c.NextAttemptAt, c.Deadline = c.CreatedAt.UTC(), c.NextAttemptAt.UTC(), c.Deadline.UTC()
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *Commands) Save(ctx context.Context, c command.Command) error {
	_, err := queryFor(ctx, r.pool).Exec(ctx, `UPDATE subsystem_commands SET status = $2, attempts = $3, next_attempt_at = $4,
		last_error = $5 WHERE command_id = $1`, c.ID, string(c.Status), c.Attempts, c.NextAttemptAt.UTC(), c.LastError)
	if err != nil {
		return fmt.Errorf("save subsystem command: %w", err)
	}
	return nil
}
