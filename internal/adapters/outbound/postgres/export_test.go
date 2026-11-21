package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Exported to the external tests only (ruling P4-R17's probes): the pool type, and the querier a repository
// reads with outside a unit of work.
type (
	Pool    = pgxpool.Pool
	Querier = querier
)

// QuerierFor exposes queryFor.
func QuerierFor(ctx context.Context, pool *pgxpool.Pool) Querier { return queryFor(ctx, pool) }
