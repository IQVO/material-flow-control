package usecases

import (
	"context"
	"errors"
	"time"

	"github.com/IQVO/material-flow-control/internal/application/command"
	"github.com/IQVO/material-flow-control/internal/application/ports"
	"github.com/IQVO/material-flow-control/internal/domain/capacity"
	"github.com/IQVO/material-flow-control/internal/domain/containertrack"
	"github.com/IQVO/material-flow-control/internal/domain/layout"
	"github.com/IQVO/material-flow-control/internal/domain/routing"
	"github.com/IQVO/material-flow-control/internal/domain/stationqueue"
	"github.com/IQVO/material-flow-control/internal/domain/transportorder"
)

// flow is one write's working set: the aggregates it loaded, what it queued and published. commit persists all
// of it in the caller's unit of work.
type flow struct {
	s       *Service
	ctx     context.Context
	now     time.Time
	orders  map[string]*transportorder.Order
	touched []string
	fresh   map[string]bool
	tracks  map[string]*containertrack.Track
	queues  map[string]*stationqueue.Queue
	events  []any
	cmds    []command.Command
	caps    []capacity.Snapshot
	ended   []*transportorder.Order
}

func (s *Service) newFlow(ctx context.Context) *flow {
	return &flow{s: s, ctx: ctx, now: s.now(), orders: map[string]*transportorder.Order{}, fresh: map[string]bool{},
		tracks: map[string]*containertrack.Track{}, queues: map[string]*stationqueue.Queue{}}
}

func (f *flow) keep(o *transportorder.Order) *transportorder.Order {
	if _, ok := f.orders[o.ID()]; !ok {
		f.touched = append(f.touched, o.ID())
	}
	f.orders[o.ID()] = o
	return o
}

func (f *flow) addNew(o *transportorder.Order) {
	f.fresh[o.ID()] = true
	f.keep(o)
}

func (f *flow) orderByID(id string) (*transportorder.Order, error) {
	if o, ok := f.orders[id]; ok {
		return o, nil
	}
	o, err := f.s.d.Orders.Get(f.ctx, id)
	if err != nil {
		return nil, err
	}
	return f.keep(o), nil
}

// orderByRef finds the order owning a leg reference; nil (no error) for a reference this context does not own.
//
//nolint:unused // Task 9 report D2: first caller lands in Task 10 (fact handlers); remove the directive there
func (f *flow) orderByRef(ref string) (*transportorder.Order, error) {
	if ref == "" {
		return nil, nil
	}
	for _, o := range f.orders {
		if _, ok := o.LegByRef(ref); ok {
			return o, nil
		}
	}
	o, err := f.s.d.Orders.ByLegRef(f.ctx, ref)
	if errors.Is(err, ports.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return f.keep(o), nil
}

// track loads a container's track, or starts one for a container seen for the first time.
func (f *flow) track(containerID string) (*containertrack.Track, error) {
	if t, ok := f.tracks[containerID]; ok {
		return t, nil
	}
	t, err := f.s.d.Tracks.Get(f.ctx, containerID)
	if errors.Is(err, ports.ErrNotFound) {
		t, err = containertrack.New(containerID, f.now), nil
	}
	if err != nil {
		return nil, err
	}
	f.tracks[containerID] = t
	return t, nil
}

// queue loads a station's queue with the layout's current buffer limit (ADR 0001).
func (f *flow) queue(stationID string) (*stationqueue.Queue, error) {
	if q, ok := f.queues[stationID]; ok {
		return q, nil
	}
	st, ok := f.s.l.Station(stationID)
	if !ok {
		return nil, ErrStationNotFound
	}
	q, err := f.s.d.Stations.Get(f.ctx, stationID)
	if errors.Is(err, ports.ErrNotFound) {
		q, err = stationqueue.New(stationID, st.BufferLimit), nil
	}
	if err != nil {
		return nil, err
	}
	q.SetBufferLimit(st.BufferLimit)
	f.queues[stationID] = q
	return q, nil
}

func (f *flow) enqueue(orderID, legRef string, kind command.Kind) {
	f.cmds = append(f.cmds, command.New("cmd-"+f.s.d.IDs.NewID(), orderID, legRef, kind, f.now, f.s.cfg.LegRequestDeadline))
}

// advance starts the order's next leg: a command, a station wait, or the completion.
func (f *flow) advance(o *transportorder.Order) error {
	leg, act := o.Next(f.now)
	switch act {
	case transportorder.ActionRequest:
		f.enqueue(o.ID(), leg.Ref, command.RequestLeg)
	case transportorder.ActionAwaitStation:
		q, err := f.queue(leg.To.StationID)
		if err != nil {
			return err
		}
		q.Join(stationqueue.Entry{OrderID: o.ID(), RequestID: o.RequestID(), ContainerID: o.ContainerID(),
			Priority: o.Priority(), DueBy: o.DueBy(), ReadyAt: f.now})
		return f.admit(q)
	case transportorder.ActionComplete:
		return f.complete(o)
	}
	return nil
}

// admit grants the station's free slots and requests the admitted orders' moves.
func (f *flow) admit(q *stationqueue.Queue) error {
	for _, occ := range q.Admit(f.now) {
		o, err := f.orderByID(occ.OrderID)
		if err != nil {
			return err
		}
		if leg, err := o.Admitted(f.now); err == nil {
			f.enqueue(o.ID(), leg.Ref, command.RequestLeg)
		}
	}
	return nil
}

func (f *flow) complete(o *transportorder.Order) error {
	pos := 0
	if dst := o.Destination(); dst.Kind == layout.PlaceStation {
		q, err := f.queue(dst.StationID)
		if err != nil {
			return err
		}
		pos = q.Position(o.ContainerID())
	}
	if o.Complete(pos, f.now) != nil {
		return nil
	}
	t, err := f.track(o.ContainerID())
	if err != nil {
		return err
	}
	t.Release(o.ID(), f.now)
	f.ended = append(f.ended, o)
	return nil
}

func confirmedLocation(p containertrack.Position) string {
	if p.Kind == containertrack.OnRobot || p.Kind == containertrack.Unknown {
		return ""
	}
	return p.Code
}

// fail ends the order with reason at the container's last confirmed location and frees its station slot.
func (f *flow) fail(o *transportorder.Order, reason transportorder.FailReason, subsystemReason string) error {
	if o.Ended() {
		return nil
	}
	t, err := f.track(o.ContainerID())
	if err != nil {
		return err
	}
	if o.Fail(reason, subsystemReason, confirmedLocation(t.Position()), f.now) != nil {
		return nil
	}
	t.Release(o.ID(), f.now)
	f.ended = append(f.ended, o)
	if dst := o.Destination(); dst.Kind == layout.PlaceStation {
		q, err := f.queue(dst.StationID)
		if err != nil {
			return err
		}
		if q.Drop(o.ID()) {
			return f.admit(q)
		}
	}
	return nil
}

// clarify holds the container (decision D12) and fails its order; a container standing on an AS/RS output port
// is moved to the clarification point by an internal order. An internal order is only failed.
//
//nolint:unused // Task 9 report D2: first caller lands in Task 10 (fact handlers); remove the directive there
func (f *flow) clarify(o *transportorder.Order, c containertrack.Clarification, at layout.Place, reason transportorder.FailReason, sub string) error {
	if o.Internal() {
		return f.fail(o, reason, sub)
	}
	t, err := f.track(o.ContainerID())
	if err != nil {
		return err
	}
	c.OrderID, c.RequestID, c.DetectedAt = o.ID(), o.RequestID(), at.Code
	ev := t.SendToClarification(c, f.now)
	if err := f.fail(o, reason, sub); err != nil {
		return err
	}
	if at.Kind == layout.PlacePort && at.PortKind == layout.PortOutput && t.Position() == containertrack.At(at) {
		clr := f.s.l.ClarificationPoint()
		plan := []routing.LegPlan{{Subsystem: routing.SubsystemAmr, SubsystemID: f.s.l.Fleet().FleetID, Kind: routing.KindMove, From: at, To: clr}}
		io, err := transportorder.NewInternal("to-"+f.s.d.IDs.NewID(), o.ContainerID(), at, clr, plan, f.now)
		if err != nil {
			return err
		}
		if t.ReserveInternal(io.ID(), f.now) == nil {
			f.addNew(io)
			ev.ClarificationPoint = clr.Code
			if err := f.advance(io); err != nil {
				return err
			}
		}
	}
	f.events = append(f.events, ev)
	return nil
}

// publishCapacity saves a snapshot and publishes it when a published field changed.
//
//nolint:unused // Task 9 report D2: first caller lands in Task 10 (fact handlers); remove the directive there
func (f *flow) publishCapacity(snap capacity.Snapshot) { f.caps = append(f.caps, snap) }
