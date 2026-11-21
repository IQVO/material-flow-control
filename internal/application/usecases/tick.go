package usecases

import (
	"context"
	"time"

	"github.com/IQVO/material-flow-control/internal/application/command"
	"github.com/IQVO/material-flow-control/internal/domain/transportorder"
)

// stale reports an order whose current leg, accepted by its subsystem, showed no progress for timeout.
func stale(o *transportorder.Order, now time.Time, timeout time.Duration) bool {
	cur, ok := o.Current()
	if !ok {
		return false
	}
	switch cur.Status {
	case transportorder.LegRequested, transportorder.LegAssigned, transportorder.LegPickedUp:
		return now.Sub(cur.UpdatedAt) >= timeout
	}
	return false
}

// Tick fails every stale transport TIMEOUT and queues the best-effort cancellation of its leg (decision D16).
func (s *Service) Tick(ctx context.Context) (int, error) {
	open, err := s.d.Orders.Open(ctx)
	if err != nil {
		return 0, err
	}
	now, n := s.now(), 0
	for _, o := range open {
		if !stale(o, now, s.cfg.LegTimeout) {
			continue
		}
		err := s.write(ctx, func(ctx context.Context) error {
			f := s.newFlow(ctx)
			fresh, err := f.orderByID(o.ID())
			if err != nil || !stale(fresh, f.now, s.cfg.LegTimeout) {
				return err
			}
			cur, _ := fresh.Current()
			f.enqueue(fresh.ID(), cur.Ref, command.CancelLeg)
			if err := f.fail(fresh, transportorder.FailTimeout, "LEG_TIMEOUT"); err != nil {
				return err
			}
			n++
			return f.commit()
		})
		if err != nil {
			return n, err
		}
	}
	return n, nil
}
