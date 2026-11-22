package transportorder

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/IQVO/material-flow-control/internal/domain/layout"
	"github.com/IQVO/material-flow-control/internal/domain/routing"
)

// ErrWrongLeg refuses a fact that does not end this kind of leg (carry, Task 6 review): Delivered, PickedUp and
// Assigned need a MOVE, Presented a RETRIEVE, Stored a STORE.
var ErrWrongLeg = errors.New("the fact does not match the leg's kind")

// LegStatus is one leg attempt's state.
type LegStatus string

const (
	LegPlanned        LegStatus = "PLANNED"
	LegWaitingStation LegStatus = "WAITING_STATION"
	LegPending        LegStatus = "PENDING"
	LegRequested      LegStatus = "REQUESTED"
	LegAssigned       LegStatus = "ASSIGNED"
	LegPickedUp       LegStatus = "PICKED_UP"
	LegDone           LegStatus = "DONE"
	LegFailed         LegStatus = "FAILED"
	LegCancelled      LegStatus = "CANCELLED"
)

// Leg is one attempt of one planned leg. Ref (`<orderId>.<seq>.<attempt>`) is the subsystem's `reference` and
// the base of every Idempotency-Key sent for it; SubsystemRef is the subsystem's own id (storage job, leg).
type Leg struct {
	Seq          int
	Attempt      int
	Ref          string
	Subsystem    routing.Subsystem
	SubsystemID  string
	Kind         routing.LegKind
	From         layout.Place
	To           layout.Place
	Status       LegStatus
	SubsystemRef string
	Robot        string
	UpdatedAt    time.Time
	EndedAt      time.Time
}

// InFlight reports a leg handed to its subsystem and not ended.
func (l Leg) InFlight() bool {
	return l.Status == LegPending || l.Status == LegRequested || l.Status == LegAssigned || l.Status == LegPickedUp
}

// Ended reports a finished attempt.
func (l Leg) Ended() bool {
	return l.Status == LegDone || l.Status == LegFailed || l.Status == LegCancelled
}

// LegByRef returns one leg attempt.
func (o *Order) LegByRef(ref string) (Leg, bool) {
	for _, l := range o.s.Legs {
		if l.Ref == ref {
			return l, true
		}
	}
	return Leg{}, false
}

func (o *Order) inFlight(ref string) (*Leg, error) {
	for i := range o.s.Legs {
		if o.s.Legs[i].Ref != ref {
			continue
		}
		if o.Ended() || !o.s.Legs[i].InFlight() {
			return nil, ErrNotInFlight
		}
		return &o.s.Legs[i], nil
	}
	return nil, ErrUnknownLeg
}

// inFlightAs is inFlight for a fact that ends or advances only one kind of leg (carry, Task 6 review).
func (o *Order) inFlightAs(ref string, kind routing.LegKind) (*Leg, error) {
	l, err := o.inFlight(ref)
	if err != nil {
		return nil, err
	}
	if l.Kind != kind {
		return nil, ErrWrongLeg
	}
	return l, nil
}

// finishAs ends a leg of one kind DONE (carry, Task 6 review).
func (o *Order) finishAs(ref string, kind routing.LegKind, at time.Time) (*Leg, error) {
	l, err := o.inFlightAs(ref, kind)
	if err != nil {
		return nil, err
	}
	l.Status, l.UpdatedAt, l.EndedAt = LegDone, at, at
	return l, nil
}

func (o *Order) finish(ref string, status LegStatus, at time.Time) (*Leg, error) {
	l, err := o.inFlight(ref)
	if err != nil {
		return nil, err
	}
	l.Status, l.UpdatedAt, l.EndedAt = status, at, at
	return l, nil
}

// Requested records the subsystem's 202 and id. It never moves a leg back: a fact may have arrived first.
func (o *Order) Requested(ref, subsystemRef string, at time.Time) error {
	for i := range o.s.Legs {
		l := &o.s.Legs[i]
		if l.Ref != ref {
			continue
		}
		if l.SubsystemRef == "" {
			l.SubsystemRef = subsystemRef
		}
		if l.Status == LegPending {
			l.Status, l.UpdatedAt = LegRequested, at
		}
		return nil
	}
	return ErrUnknownLeg
}

// ResolveTo fixes the input port of a MOVE into the AS/RS when it is dispatched (ADR 0002).
func (o *Order) ResolveTo(ref string, p layout.Place) error {
	l, err := o.inFlight(ref)
	if err != nil {
		return err
	}
	if !l.To.IsZero() {
		return ErrNotInFlight
	}
	l.To = p
	return nil
}

// Assigned records the robot of an AMR leg.
func (o *Order) Assigned(ref, robot string, at time.Time) error {
	l, err := o.inFlightAs(ref, routing.KindMove) // carry (Task 6 review)
	if err != nil {
		return err
	}
	if l.Status == LegPending || l.Status == LegRequested {
		l.Status = LegAssigned
	}
	l.Robot, l.UpdatedAt = robot, at
	return nil
}

// PickedUp records that the container is on the robot.
func (o *Order) PickedUp(ref, robot string, at time.Time) error {
	l, err := o.inFlightAs(ref, routing.KindMove) // carry (Task 6 review)
	if err != nil {
		return err
	}
	l.Status, l.Robot, l.UpdatedAt = LegPickedUp, robot, at
	return nil
}

// Presented ends a RETRIEVE at the output port it was presented at.
func (o *Order) Presented(ref string, port layout.Place, at time.Time) error {
	l, err := o.finishAs(ref, routing.KindRetrieve, at) // carry (Task 6 review)
	if err != nil {
		return err
	}
	l.To = port
	return nil
}

// Delivered ends a MOVE at its destination (amr-fleet-control LegCompleted).
func (o *Order) Delivered(ref string, at time.Time) error {
	_, err := o.finishAs(ref, routing.KindMove, at) // carry (Task 6 review)
	return err
}

// Stored ends a STORE (asrs-control ContainerStored).
func (o *Order) Stored(ref string, at time.Time) error {
	_, err := o.finishAs(ref, routing.KindStore, at) // carry (Task 6 review)
	return err
}

// LegFailed ends an attempt without its outcome; the use case retries, re-plans or fails the order.
func (o *Order) LegFailed(ref string, at time.Time) error {
	_, err := o.finish(ref, LegFailed, at)
	return err
}

// LegFailedAs is LegFailed for a fact that ends only some kinds of leg (amr-fleet-control's LegFailed a MOVE,
// asrs-control's StorageJobFailed a RETRIEVE or a STORE): a known leg of another kind is refused with ErrWrongLeg
// and left unchanged, whether or not its order ended. carry (Task 10 review)
func (o *Order) LegFailedAs(ref string, at time.Time, kinds ...routing.LegKind) error {
	if l, ok := o.LegByRef(ref); ok && !slices.Contains(kinds, l.Kind) {
		return ErrWrongLeg
	}
	return o.LegFailed(ref, at)
}

// RetryLeg adds the next attempt of a failed MOVE (decision D13), PENDING, right after the failed one.
func (o *Order) RetryLeg(ref string, at time.Time) (Leg, error) {
	for i, l := range o.s.Legs {
		if l.Ref != ref {
			continue
		}
		if o.Ended() || l.Status != LegFailed || l.Kind != routing.KindMove {
			return Leg{}, ErrNotRetryable
		}
		next := l
		next.Attempt++
		next.Ref = fmt.Sprintf("%s.%d.%d", o.s.ID, l.Seq, next.Attempt)
		next.Status, next.SubsystemRef, next.Robot, next.UpdatedAt, next.EndedAt = LegPending, "", "", at, time.Time{}
		o.s.Legs = append(o.s.Legs[:i+1], append([]Leg{next}, o.s.Legs[i+1:]...)...)
		o.s.Retries++
		return next, nil
	}
	return Leg{}, ErrUnknownLeg
}

// Replan appends the legs of a re-plan after a failed STORE (decision D13).
func (o *Order) Replan(ref string, plan []routing.LegPlan, at time.Time) error {
	l, ok := o.LegByRef(ref)
	if !ok {
		return ErrUnknownLeg
	}
	if o.Ended() || l.Status != LegFailed || l.Kind != routing.KindStore || len(plan) == 0 {
		return ErrNotRetryable
	}
	o.appendLegs(plan, at)
	o.s.Replans++
	return nil
}
