package usecases

import (
	"context"
	"sort"

	"github.com/IQVO/material-flow-control/internal/domain/capacity"
	"github.com/IQVO/material-flow-control/internal/domain/transportorder"
)

// Deviation (Task 9 report D4): the brief's flow.go is 312 code lines after the D1 split; sortedKeys, commit,
// saveAggregates, saveCapacities and measure moved here unchanged to keep each file under the 300-line budget.

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// commit persists the working set: orders, tracks, queues (and their station capacity), events in order —
// the flow's own, then each order's, then capacity — and the queued commands; metrics run after commit.
// Deviation (Task 9 report D1): the brief's single body is split into saveAggregates and saveCapacities
// (gocognit 25 > 20); the statements and their order are unchanged.
func (f *flow) commit() error {
	d := f.s.d
	if err := f.saveAggregates(); err != nil {
		return err
	}
	events := append([]any(nil), f.events...)
	for _, id := range f.touched {
		events = append(events, f.orders[id].PullEvents()...)
	}
	events, err := f.saveCapacities(events)
	if err != nil {
		return err
	}
	if len(events) > 0 {
		if err := d.Sink.Append(f.ctx, events...); err != nil {
			return err
		}
	}
	for _, c := range f.cmds {
		if err := d.Commands.Enqueue(f.ctx, c); err != nil {
			return err
		}
	}
	f.measure()
	return nil
}

// saveAggregates persists the touched orders, then the tracks and queues by id, and records each queue's
// station capacity snapshot.
func (f *flow) saveAggregates() error {
	d := f.s.d
	for _, id := range f.touched {
		save := d.Orders.Save
		if f.fresh[id] {
			save = d.Orders.Insert
		}
		if err := save(f.ctx, f.orders[id]); err != nil {
			return err
		}
	}
	for _, id := range sortedKeys(f.tracks) {
		if err := d.Tracks.Save(f.ctx, f.tracks[id]); err != nil {
			return err
		}
	}
	for _, id := range sortedKeys(f.queues) {
		q := f.queues[id]
		if err := d.Stations.Save(f.ctx, q); err != nil {
			return err
		}
		f.caps = append(f.caps, capacity.Station(id, q.BufferLimit(), q.Occupied(), len(q.Waiting()), f.now))
	}
	return nil
}

// saveCapacities saves every snapshot and appends a SubsystemCapacityChanged to events for each that changed.
func (f *flow) saveCapacities(events []any) ([]any, error) {
	d := f.s.d
	for _, snap := range f.caps {
		prev, found, err := d.Capacities.Get(f.ctx, snap.Kind, snap.ID)
		if err != nil {
			return nil, err
		}
		if err := d.Capacities.Save(f.ctx, snap); err != nil {
			return nil, err
		}
		if ev, changed := capacity.Changed(prev, found, snap); changed {
			events = append(events, ev)
		}
	}
	return events, nil
}

func (f *flow) measure() {
	if f.s.d.Metrics == nil {
		return
	}
	for _, o := range f.ended {
		if o.Internal() {
			continue
		}
		s := o.Snapshot()
		outcome, reason := "completed", ""
		if s.Status == transportorder.StatusFailed {
			outcome, reason = "failed", string(s.Reason)
		}
		purpose, took, ctx := string(s.Purpose), s.EndedAt.Sub(s.AcceptedAt), context.WithoutCancel(f.ctx)
		f.s.d.UoW.AfterCommit(f.ctx, func() { f.s.d.Metrics.TransportEnded(ctx, purpose, outcome, reason, took) })
	}
}
