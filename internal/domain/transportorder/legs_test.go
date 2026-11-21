package transportorder_test

import (
	"errors"
	"testing"
	"time"

	"github.com/IQVO/material-flow-control/internal/domain/routing"
	to "github.com/IQVO/material-flow-control/internal/domain/transportorder"
	tr "github.com/IQVO/material-flow-control/internal/domain/transportrequest"
)

func TestAFailedMoveIsRetriedAsANewAttemptWithItsOwnReference(t *testing.T) {
	o := accept(t, tr.PurposePutaway, decant, asrs, putawayPlan())
	first, _ := o.Next(t0)
	must(t, o.Requested(first.Ref, "leg-a", t0))
	must(t, o.LegFailed(first.Ref, t0.Add(time.Minute)))
	retry, err := o.RetryLeg(first.Ref, t0.Add(time.Minute))
	if err != nil || retry.Ref != "to-1.1.2" || retry.Seq != 1 || retry.Attempt != 2 || retry.Status != to.LegPending || retry.From != decant || retry.SubsystemRef != "" {
		t.Fatalf("retry = %+v %v", retry, err)
	}
	if o.Retries() != 1 {
		t.Fatalf("retries = %d", o.Retries())
	}
	if _, act := o.Next(t0); act != to.ActionNone {
		t.Fatalf("Next while the retry is in flight = %s", act)
	}
	if cur, ok := o.Current(); !ok || cur.Ref != "to-1.1.2" {
		t.Fatalf("current = %+v", cur)
	}
	if _, err := o.RetryLeg(retry.Ref, t0); !errors.Is(err, to.ErrNotRetryable) {
		t.Fatalf("retrying a leg in flight = %v", err)
	}
}

func TestOnlyAFailedMoveIsRetryable(t *testing.T) {
	o := accept(t, tr.PurposePick, asrs, station, pickPlan())
	r, _ := o.Next(t0)
	must(t, o.LegFailed(r.Ref, t0))
	if _, err := o.RetryLeg(r.Ref, t0); !errors.Is(err, to.ErrNotRetryable) {
		t.Fatalf("a failed retrieve = %v", err)
	}
}

func TestAFailedStoreIsReplannedOnce(t *testing.T) {
	o := accept(t, tr.PurposePutaway, decant, asrs, putawayPlan())
	move, _ := o.Next(t0)
	must(t, o.ResolveTo(move.Ref, in1))
	must(t, o.Delivered(move.Ref, t0))
	store, _ := o.Next(t0)
	must(t, o.LegFailed(store.Ref, t0.Add(time.Minute)))
	replan := []routing.LegPlan{
		{Subsystem: routing.SubsystemAmr, SubsystemID: "AMR1", Kind: routing.KindMove, From: in1, To: in2},
		{Subsystem: routing.SubsystemAsrs, SubsystemID: "ASRS1", Kind: routing.KindStore, From: in2, To: asrs},
	}
	must(t, o.Replan(store.Ref, replan, t0.Add(time.Minute)))
	next, act := o.Next(t0.Add(time.Minute))
	if act != to.ActionRequest || next.Ref != "to-1.3.1" || next.From != in1 || next.To != in2 {
		t.Fatalf("after the re-plan = %+v %s", next, act)
	}
	must(t, o.Delivered(next.Ref, t0))
	s2, _ := o.Next(t0)
	if s2.Ref != "to-1.4.1" || s2.From != in2 {
		t.Fatalf("second store = %+v", s2)
	}
	if o.Replans() != 1 {
		t.Fatalf("replans = %d", o.Replans())
	}
	if err := o.Replan(next.Ref, replan, t0); !errors.Is(err, to.ErrNotRetryable) {
		t.Fatalf("re-planning a delivered move = %v", err)
	}
}

func TestFactsAreAcceptedInAnyOrderTheSubsystemCanSendThem(t *testing.T) {
	o := accept(t, tr.PurposeOutbound, decant, pack, []routing.LegPlan{{Subsystem: routing.SubsystemAmr, SubsystemID: "AMR1", Kind: routing.KindMove, From: decant, To: pack}})
	leg, _ := o.Next(t0)
	must(t, o.Assigned(leg.Ref, "EMU/R2", t0))
	must(t, o.Requested(leg.Ref, "leg-z", t0))
	l, _ := o.LegByRef(leg.Ref)
	if l.Status != to.LegAssigned || l.SubsystemRef != "leg-z" || l.Robot != "EMU/R2" {
		t.Fatalf("a late 202 must not move the leg back: %+v", l)
	}
	must(t, o.Delivered(leg.Ref, t0))
	if err := o.Assigned(leg.Ref, "EMU/R2", t0); !errors.Is(err, to.ErrNotInFlight) {
		t.Fatalf("a fact after delivery = %v", err)
	}
	if err := o.Requested(leg.Ref, "leg-z", t0); err != nil {
		t.Fatalf("a 202 recorded after delivery is harmless: %v", err)
	}
	if _, err := o.Admitted(t0); !errors.Is(err, to.ErrNotWaiting) {
		t.Fatalf("Admitted without a waiting leg = %v", err)
	}
}

// carry (Task 11 review): a subsystem may accept a leg after its order ended (the dispatcher calls outside the unit
// of work); Requested records the subsystem's id on the CANCELLED leg so the use case can cancel it, and moves no
// status.
func TestALegAcceptedAfterTheOrderEndedKeepsItsSubsystemID(t *testing.T) {
	o := accept(t, tr.PurposePutaway, decant, asrs, putawayPlan())
	first, _ := o.Next(t0)
	must(t, o.Fail(to.FailCancelled, "", "", t0))
	must(t, o.Requested(first.Ref, "leg-a", t0.Add(time.Minute)))
	if l, _ := o.LegByRef(first.Ref); l.Status != to.LegCancelled || l.SubsystemRef != "leg-a" || !l.UpdatedAt.Equal(first.UpdatedAt) {
		t.Fatalf("leg = %+v", l)
	}
}
