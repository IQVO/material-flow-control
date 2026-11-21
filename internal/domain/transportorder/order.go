// Package transportorder is the TransportOrder aggregate: one accepted request's legs through handoff points, in
// order (responsibility 2: a leg starts only after the previous leg's handoff fact), and its ONE outcome
// (responsibility 5).
package transportorder

import (
	"errors"
	"fmt"
	"time"

	"github.com/IQVO/material-flow-control/internal/domain/layout"
	"github.com/IQVO/material-flow-control/internal/domain/routing"
	"github.com/IQVO/material-flow-control/internal/domain/transportrequest"
)

var (
	ErrNoLegs         = errors.New("a transport order needs at least one leg")
	ErrEnded          = errors.New("the transport order has ended")
	ErrUnknownLeg     = errors.New("no leg of this order has that reference")
	ErrNotInFlight    = errors.New("the leg is not in flight")
	ErrNotWaiting     = errors.New("no leg waits for a station slot")
	ErrNotCancellable = errors.New("the transport cannot be cancelled now")
	ErrNotRetryable   = errors.New("the leg cannot be retried or re-planned")
)

// Status is the order's lifecycle state.
type Status string

const (
	StatusInProgress Status = "IN_PROGRESS"
	StatusCompleted  Status = "COMPLETED"
	StatusFailed     Status = "FAILED"
)

// FailReason is TransportFailed's reason (seam decision 3).
type FailReason string

const (
	FailClarification  FailReason = "CLARIFICATION"
	FailSubsystemFault FailReason = "SUBSYSTEM_FAULT"
	FailCancelled      FailReason = "CANCELLED"
	FailTimeout        FailReason = "TIMEOUT"
)

// Action is what Next asks the use case to do.
type Action string

const (
	ActionNone         Action = ""
	ActionRequest      Action = "REQUEST"
	ActionAwaitStation Action = "AWAIT_STATION"
	ActionComplete     Action = "COMPLETE"
)

// CancelDecision is RequestCancel's answer.
type CancelDecision string

const (
	CancelNow          CancelDecision = "NOW"
	CancelAsk          CancelDecision = "ASK"
	CancelAlreadyAsked CancelDecision = "ALREADY_ASKED"
)

// Snapshot is the persisted state.
type Snapshot struct {
	ID                string
	RequestID         string
	ContainerID       string
	Source            layout.Place
	Destination       layout.Place
	Purpose           transportrequest.Purpose
	Priority          transportrequest.Priority
	DueBy             time.Time
	Internal          bool
	Legs              []Leg
	Status            Status
	Reason            FailReason
	SubsystemReason   string
	ContainerLocation string
	CancelRequested   bool
	CancelRefused     bool
	Retries           int
	Replans           int
	AcceptedAt        time.Time
	EndedAt           time.Time
	Version           int64
}

// Order is one transport order.
type Order struct {
	s      Snapshot
	events []any
}

func build(s Snapshot, plan []routing.LegPlan, at time.Time) (*Order, error) {
	if len(plan) == 0 {
		return nil, ErrNoLegs
	}
	o := &Order{s: s}
	o.appendLegs(plan, at)
	return o, nil
}

// Accept creates the order of an accepted request and raises TransportAccepted.
func Accept(id string, req transportrequest.Request, src, dst layout.Place, plan []routing.LegPlan, at time.Time) (*Order, error) {
	o, err := build(Snapshot{ID: id, RequestID: req.ID, ContainerID: req.ContainerID, Source: src, Destination: dst,
		Purpose: req.Purpose, Priority: req.Priority, DueBy: req.DueBy, Status: StatusInProgress, AcceptedAt: at}, plan, at)
	if err != nil {
		return nil, err
	}
	o.events = append(o.events, TransportAccepted{OrderID: id, RequestID: req.ID, ContainerID: req.ContainerID, Source: src.Code,
		Destination: dst.Code, Purpose: req.Purpose, Priority: req.Priority, DueBy: req.DueBy, Legs: len(plan), At: at})
	return o, nil
}

// NewInternal creates an internal order (a clarification move): it publishes no transport event.
func NewInternal(id, containerID string, src, dst layout.Place, plan []routing.LegPlan, at time.Time) (*Order, error) {
	return build(Snapshot{ID: id, ContainerID: containerID, Source: src, Destination: dst, Internal: true,
		Priority: transportrequest.PriorityUrgent, Status: StatusInProgress, AcceptedAt: at}, plan, at)
}

func clone(s Snapshot) Snapshot {
	s.Legs = append([]Leg(nil), s.Legs...)
	return s
}

// Restore rebuilds an order from its snapshot.
func Restore(s Snapshot) *Order { return &Order{s: clone(s)} }

// Snapshot returns a copy of the state.
func (o *Order) Snapshot() Snapshot { return clone(o.s) }

func (o *Order) ID() string                          { return o.s.ID }
func (o *Order) RequestID() string                   { return o.s.RequestID }
func (o *Order) ContainerID() string                 { return o.s.ContainerID }
func (o *Order) Source() layout.Place                { return o.s.Source }
func (o *Order) Destination() layout.Place           { return o.s.Destination }
func (o *Order) Purpose() transportrequest.Purpose   { return o.s.Purpose }
func (o *Order) Priority() transportrequest.Priority { return o.s.Priority }
func (o *Order) DueBy() time.Time                    { return o.s.DueBy }
func (o *Order) Internal() bool                      { return o.s.Internal }
func (o *Order) Status() Status                      { return o.s.Status }
func (o *Order) Reason() FailReason                  { return o.s.Reason }
func (o *Order) Ended() bool                         { return o.s.Status != StatusInProgress }
func (o *Order) Version() int64                      { return o.s.Version }
func (o *Order) Retries() int                        { return o.s.Retries }
func (o *Order) Replans() int                        { return o.s.Replans }
func (o *Order) CancelRequested() bool               { return o.s.CancelRequested }

// Legs returns a copy of every leg attempt in order.
func (o *Order) Legs() []Leg { return append([]Leg(nil), o.s.Legs...) }

// PullEvents returns and clears the raised events.
func (o *Order) PullEvents() []any {
	e := o.events
	o.events = nil
	return e
}

func (o *Order) appendLegs(plan []routing.LegPlan, at time.Time) {
	seq := 0
	for _, l := range o.s.Legs {
		seq = max(seq, l.Seq)
	}
	for _, p := range plan {
		seq++
		o.s.Legs = append(o.s.Legs, Leg{Seq: seq, Attempt: 1, Ref: fmt.Sprintf("%s.%d.1", o.s.ID, seq), Subsystem: p.Subsystem,
			SubsystemID: p.SubsystemID, Kind: p.Kind, From: p.From, To: p.To, Status: LegPlanned, UpdatedAt: at})
	}
}

// Current is the first leg that has not ended.
func (o *Order) Current() (Leg, bool) {
	for _, l := range o.s.Legs {
		if !l.Ended() {
			return l, true
		}
	}
	return Leg{}, false
}

// Previous is the last DONE leg before the leg with ref.
func (o *Order) Previous(ref string) (Leg, bool) {
	var prev Leg
	found := false
	for _, l := range o.s.Legs {
		if l.Ref == ref {
			return prev, found
		}
		if l.Status == LegDone {
			prev, found = l, true
		}
	}
	return Leg{}, false
}

// Next starts the next leg once the previous one is DONE: a MOVE into a station waits for a slot
// (ActionAwaitStation), any other leg is requested (ActionRequest); ActionComplete when every leg is done.
func (o *Order) Next(at time.Time) (Leg, Action) {
	if o.Ended() {
		return Leg{}, ActionNone
	}
	var prev *Leg
	for i := range o.s.Legs {
		l := &o.s.Legs[i]
		if l.Status == LegDone {
			prev = l
			continue
		}
		if l.Status != LegPlanned {
			if l.Ended() {
				continue
			}
			return Leg{}, ActionNone
		}
		return o.activate(l, prev, at)
	}
	return Leg{}, ActionComplete
}

func (o *Order) activate(l *Leg, prev *Leg, at time.Time) (Leg, Action) {
	if l.From.IsZero() && prev != nil {
		l.From = prev.To
	}
	l.UpdatedAt = at
	if l.Kind == routing.KindMove && l.To.Kind == layout.PlaceStation && !o.s.Internal {
		l.Status = LegWaitingStation
		return *l, ActionAwaitStation
	}
	l.Status = LegPending
	return *l, ActionRequest
}

// Admitted moves the leg waiting for a station slot to PENDING.
func (o *Order) Admitted(at time.Time) (Leg, error) {
	for i := range o.s.Legs {
		if o.s.Legs[i].Status == LegWaitingStation {
			o.s.Legs[i].Status, o.s.Legs[i].UpdatedAt = LegPending, at
			return o.s.Legs[i], nil
		}
	}
	return Leg{}, ErrNotWaiting
}

// Complete ends the order at its destination. A station destination also raises ContainerArrivedAtStation.
func (o *Order) Complete(queuePosition int, at time.Time) error {
	if o.Ended() {
		return ErrEnded
	}
	o.s.Status, o.s.EndedAt = StatusCompleted, at
	if o.s.Internal {
		return nil
	}
	if o.s.Destination.Kind == layout.PlaceStation {
		o.events = append(o.events, ContainerArrivedAtStation{OrderID: o.s.ID, RequestID: o.s.RequestID, ContainerID: o.s.ContainerID,
			StationID: o.s.Destination.StationID, Purpose: o.s.Purpose, Priority: o.s.Priority, DueBy: o.s.DueBy, QueuePosition: queuePosition, At: at})
	}
	o.events = append(o.events, TransportCompleted{OrderID: o.s.ID, RequestID: o.s.RequestID, ContainerID: o.s.ContainerID,
		Destination: o.s.Destination.Code, Purpose: o.s.Purpose, At: at})
	return nil
}

// Fail ends the order with reason; every leg that has not ended is abandoned (CANCELLED).
func (o *Order) Fail(reason FailReason, subsystemReason, location string, at time.Time) error {
	if o.Ended() {
		return ErrEnded
	}
	for i := range o.s.Legs {
		if !o.s.Legs[i].Ended() {
			o.s.Legs[i].Status, o.s.Legs[i].EndedAt = LegCancelled, at
		}
	}
	o.s.Status, o.s.Reason, o.s.SubsystemReason, o.s.ContainerLocation, o.s.EndedAt = StatusFailed, reason, subsystemReason, location, at
	if !o.s.Internal {
		o.events = append(o.events, TransportFailed{OrderID: o.s.ID, RequestID: o.s.RequestID, ContainerID: o.s.ContainerID, Purpose: o.s.Purpose,
			Reason: reason, SubsystemReason: subsystemReason, ContainerLocation: location, At: at})
	}
	return nil
}

// RequestCancel decides a cancellation (decision D15): at once between legs at a handoff that is not an AS/RS
// output port; ask the subsystem for a leg in flight before the pickup; refuse with the container on a robot or
// on an output port (ruling P4-R6: whatever the status of a current leg starting there).
func (o *Order) RequestCancel(at time.Time) (CancelDecision, Leg, error) {
	if o.Ended() {
		return "", Leg{}, ErrEnded
	}
	cur, _ := o.Current()
	if o.s.CancelRequested {
		return CancelAlreadyAsked, cur, nil
	}
	// ruling P4-R6: a leg not yet picked up from an AS/RS output port leaves the container on the port.
	if cur.From.Kind == layout.PlacePort && cur.From.PortKind == layout.PortOutput {
		return "", cur, ErrNotCancellable
	}
	switch cur.Status {
	case LegPlanned, LegWaitingStation:
		if prev, ok := o.Previous(cur.Ref); ok && prev.Kind == routing.KindRetrieve {
			return "", cur, ErrNotCancellable
		}
		return CancelNow, cur, nil
	case LegPending, LegRequested, LegAssigned:
		o.s.CancelRequested = true
		return CancelAsk, cur, nil
	default:
		return "", cur, ErrNotCancellable
	}
}

// CancelRefused records that the subsystem refused the cancellation: the transport runs to its outcome.
func (o *Order) CancelRefused(_ time.Time) {
	o.s.CancelRequested, o.s.CancelRefused = false, true
}
