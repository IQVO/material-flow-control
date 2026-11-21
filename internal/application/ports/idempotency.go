package ports

import (
	"context"
	"time"

	"github.com/IQVO/material-flow-control/internal/application/idempotency"
)

// IdempotencyStore keeps the answers for the 24-hour window: Lookup ignores a record created
// before notBefore, Save keeps the first writer of a key unless that row is older than
// notBefore (then it is replaced), Purge deletes rows created before before.
type IdempotencyStore interface {
	Lookup(ctx context.Context, key string, notBefore time.Time) (idempotency.IdempotencyRecord, bool, error)
	Save(ctx context.Context, rec idempotency.IdempotencyRecord, notBefore time.Time) error
	Purge(ctx context.Context, before time.Time) (int64, error)
}
