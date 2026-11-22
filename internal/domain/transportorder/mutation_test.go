package transportorder_test

import (
	"testing"

	"github.com/IQVO/material-flow-control/internal/domain/routing"
	to "github.com/IQVO/material-flow-control/internal/domain/transportorder"
	tr "github.com/IQVO/material-flow-control/internal/domain/transportrequest"
)

// carry (Task 6 review, Task 26 mutation work): Assigned moves a REQUESTED leg to ASSIGNED with its robot (kills the
// legs.go Status == LegRequested negation); a late Assigned after the pickup keeps PICKED_UP and records the robot.
func TestAssignedOnARequestedLegAndALateAssignedAfterThePickup(t *testing.T) {
	o := accept(t, tr.PurposePutaway, decant, asrs, putawayPlan())
	move, _ := o.Next(t0)
	must(t, o.Requested(move.Ref, "leg-1", t0))
	must(t, o.Assigned(move.Ref, "EMU/R1", t0))
	if l, _ := o.LegByRef(move.Ref); l.Status != to.LegAssigned || l.Robot != "EMU/R1" {
		t.Fatalf("Assigned on REQUESTED = %s robot %q, want ASSIGNED EMU/R1", l.Status, l.Robot)
	}
	must(t, o.PickedUp(move.Ref, "EMU/R1", t0))
	must(t, o.Assigned(move.Ref, "EMU/R2", t0))
	if l, _ := o.LegByRef(move.Ref); l.Status != to.LegPickedUp || l.Robot != "EMU/R2" {
		t.Fatalf("late Assigned after the pickup = %s robot %q, want PICKED_UP EMU/R2", l.Status, l.Robot)
	}
}

// ruling P4-R7, Task 26 mutation work: a FAILED STORE is superseded by appended legs only when it was re-planned
// (Replans > 0). Restored with a later leg but Replans 0 it still stops the order (kills Replans > 0 -> >= 0).
func TestAFailedStoreWithALaterLegButNoReplanStopsTheOrder(t *testing.T) {
	o := accept(t, tr.PurposePutaway, decant, asrs, putawayPlan())
	move, _ := o.Next(t0)
	must(t, o.ResolveTo(move.Ref, in1))
	must(t, o.Delivered(move.Ref, t0))
	store, _ := o.Next(t0)
	must(t, o.LegFailed(store.Ref, t0))
	s := o.Snapshot()
	later := s.Legs[len(s.Legs)-1]
	later.Seq, later.Attempt, later.Ref, later.Status, later.Kind = later.Seq+1, 1, "to-1.3.1", to.LegPlanned, routing.KindMove
	s.Legs, s.Replans = append(s.Legs, later), 0
	if _, act := to.Restore(s).Next(t0); act != to.ActionNone {
		t.Fatalf("Next = %s, want NONE: a FAILED STORE that was not re-planned is not superseded", act)
	}
}

// ruling P4-R6: only an OUTPUT port refuses the cancellation; a planned MOVE from an INPUT port (the shape of a
// re-planned store's first leg) is cancelled at once.
func TestAPlannedMoveFromAnInputPortIsCancelledAtOnce(t *testing.T) {
	o := accept(t, tr.PurposeOutbound, in1, pack, []routing.LegPlan{
		{Subsystem: routing.SubsystemAmr, SubsystemID: "AMR1", Kind: routing.KindMove, From: in1, To: pack}})
	if d, _, err := o.RequestCancel(t0); err != nil || d != to.CancelNow {
		t.Fatalf("RequestCancel = %s, %v; want NOW", d, err)
	}
}

// carry (Task 6 review): a refused cancellation is recorded (CancelRefused true) and clears the pending ask, so a
// second RequestCancel asks again (asrs job-not-cancellable is RETRYABLE).
func TestARefusedCancellationIsRecordedAndCanBeAskedAgain(t *testing.T) {
	o := accept(t, tr.PurposePutaway, decant, asrs, putawayPlan())
	o.Next(t0)
	if d, _, err := o.RequestCancel(t0); err != nil || d != to.CancelAsk || !o.CancelRequested() {
		t.Fatalf("first RequestCancel = %s, %v", d, err)
	}
	o.CancelRefused(t0)
	if s := o.Snapshot(); !s.CancelRefused || s.CancelRequested {
		t.Fatalf("after CancelRefused: refused %v requested %v", s.CancelRefused, s.CancelRequested)
	}
	if d, _, err := o.RequestCancel(t0); err != nil || d != to.CancelAsk {
		t.Fatalf("second RequestCancel = %s, %v; want ASK again", d, err)
	}
}

// Task 26 mutation work: between legs, only a RETRIEVE before the planned leg refuses the cancellation; a putaway's
// STORE planned after its delivered MOVE is cancelled at once (kills prev.Kind == KindRetrieve negated).
func TestAStorePlannedAfterItsDeliveredMoveIsCancelledAtOnce(t *testing.T) {
	o := accept(t, tr.PurposePutaway, decant, asrs, putawayPlan())
	move, _ := o.Next(t0)
	must(t, o.ResolveTo(move.Ref, in1))
	must(t, o.Delivered(move.Ref, t0))
	if d, cur, err := o.RequestCancel(t0); err != nil || d != to.CancelNow || cur.Kind != routing.KindStore {
		t.Fatalf("RequestCancel = %s %+v, %v; want NOW for the planned STORE", d, cur, err)
	}
}
