package transportorder_test

import (
	"errors"
	"testing"
	"time"

	"github.com/IQVO/material-flow-control/internal/domain/routing"
	to "github.com/IQVO/material-flow-control/internal/domain/transportorder"
	tr "github.com/IQVO/material-flow-control/internal/domain/transportrequest"
)

// ruling P4-R6 (D15 read by intent): while the container sits on an AS/RS OUTPUT port no robot has picked it up
// yet, so the transport is not cancellable whatever the current leg's status (a cancel would end it with the tote
// on OUT1, no CONFIRM_PICKUP and the port blocked). Nothing is recorded: CancelRequested stays false.
func TestAMoveFromAnOutputPortIsNotCancellableBeforeThePickup(t *testing.T) {
	o := accept(t, tr.PurposePick, asrs, station, pickPlan())
	o.Next(t0)
	must(t, o.Presented("to-1.1.1", out1, t0))
	o.Next(t0)
	if _, err := o.Admitted(t0); err != nil {
		t.Fatal(err)
	}
	if d, _, err := o.RequestCancel(t0); !errors.Is(err, to.ErrNotCancellable) || o.CancelRequested() {
		t.Fatalf("PENDING move from OUT1 = %s %v, cancel requested %v", d, err, o.CancelRequested())
	}
	must(t, o.Requested("to-1.2.1", "leg-a", t0))
	must(t, o.Assigned("to-1.2.1", "EMU/R1", t0))
	if d, _, err := o.RequestCancel(t0); !errors.Is(err, to.ErrNotCancellable) || o.CancelRequested() {
		t.Fatalf("ASSIGNED move from OUT1 = %s %v", d, err)
	}
}

// ruling P4-R6: a retried MOVE from the output port is still not cancellable.
func TestARetriedMoveFromAnOutputPortIsNotCancellable(t *testing.T) {
	o := accept(t, tr.PurposePick, asrs, station, pickPlan())
	o.Next(t0)
	must(t, o.Presented("to-1.1.1", out1, t0))
	o.Next(t0)
	if _, err := o.Admitted(t0); err != nil {
		t.Fatal(err)
	}
	must(t, o.LegFailed("to-1.2.1", t0.Add(time.Minute)))
	retry, err := o.RetryLeg("to-1.2.1", t0.Add(time.Minute))
	if err != nil || retry.From != out1 {
		t.Fatalf("retry = %+v %v", retry, err)
	}
	if d, _, err := o.RequestCancel(t0); !errors.Is(err, to.ErrNotCancellable) || o.CancelRequested() {
		t.Fatalf("retried move from OUT1 = %s %v", d, err)
	}
}

// ruling P4-R7: the aggregate enforces invariant 2 itself. A FAILED leg nobody retried or re-planned blocks the
// order: Next starts nothing after it (ActionNone) and the order cannot complete.
func TestAFailedLegThatWasNeitherRetriedNorReplannedStopsTheOrder(t *testing.T) {
	o := accept(t, tr.PurposePutaway, decant, asrs, putawayPlan())
	move, _ := o.Next(t0)
	must(t, o.LegFailed(move.Ref, t0.Add(time.Minute)))
	if leg, act := o.Next(t0.Add(time.Minute)); act != to.ActionNone {
		t.Fatalf("Next after an unhandled failed move = %+v %s, want ActionNone", leg, act)
	}
	if l := o.Legs()[1]; l.Status != to.LegPlanned {
		t.Fatalf("the store started: %+v", l)
	}
	if err := o.Complete(0, t0); !errors.Is(err, to.ErrLegsRemaining) || o.Ended() {
		t.Fatalf("Complete with a FAILED move + PLANNED store = %v, ended %v", err, o.Ended())
	}
	s := o.Snapshot()
	s.Legs[1].Status = to.LegPending
	r := to.Restore(s)
	if err := r.Complete(0, t0); !errors.Is(err, to.ErrLegsRemaining) || r.Ended() || len(r.PullEvents()) != 0 {
		t.Fatalf("Complete with a FAILED move + PENDING store = %v, ended %v", err, r.Ended())
	}
}

// ruling P4-R7: a FAILED attempt a later attempt of the same leg replaced, or a FAILED STORE a re-plan replaced,
// is skipped, and the order completes once the replacing legs are done.
func TestAReplacedFailedLegIsSkippedAndTheOrderCompletes(t *testing.T) {
	o := accept(t, tr.PurposePutaway, decant, asrs, putawayPlan())
	move, _ := o.Next(t0)
	must(t, o.LegFailed(move.Ref, t0))
	retry, err := o.RetryLeg(move.Ref, t0)
	must(t, err)
	must(t, o.ResolveTo(retry.Ref, in1))
	must(t, o.Delivered(retry.Ref, t0))
	store, act := o.Next(t0)
	if act != to.ActionRequest || store.Ref != "to-1.2.1" || store.From != in1 {
		t.Fatalf("after the retried move = %+v %s", store, act)
	}
	must(t, o.LegFailed(store.Ref, t0))
	must(t, o.Replan(store.Ref, []routing.LegPlan{
		{Subsystem: routing.SubsystemAmr, SubsystemID: "AMR1", Kind: routing.KindMove, From: in1, To: in2},
		{Subsystem: routing.SubsystemAsrs, SubsystemID: "ASRS1", Kind: routing.KindStore, From: in2, To: asrs},
	}, t0))
	for _, ref := range []string{"to-1.3.1", "to-1.4.1"} {
		leg, act := o.Next(t0)
		if act != to.ActionRequest || leg.Ref != ref {
			t.Fatalf("re-plan leg = %+v %s, want %s", leg, act, ref)
		}
		must(t, o.Delivered(ref, t0))
	}
	if _, act := o.Next(t0); act != to.ActionComplete {
		t.Fatalf("Next after the re-plan = %s", act)
	}
	if err := o.Complete(0, t0); err != nil || o.Status() != to.StatusCompleted {
		t.Fatalf("Complete = %v %s", err, o.Status())
	}
}
