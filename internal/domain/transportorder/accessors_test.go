package transportorder_test

import (
	"errors"
	"testing"

	"github.com/IQVO/material-flow-control/internal/domain/routing"
	to "github.com/IQVO/material-flow-control/internal/domain/transportorder"
	tr "github.com/IQVO/material-flow-control/internal/domain/transportrequest"
)

// carry (Task 6 review): the accessors return what Accept recorded, and every leg method refuses an unknown
// reference or an ended order without changing a leg.
func TestTheAccessorsAndTheRefusalsOfUnknownLegsAndEndedOrders(t *testing.T) {
	o := accept(t, tr.PurposePick, asrs, station, pickPlan())
	r := req(tr.PurposePick, asrs, station)
	if o.RequestID() != "rq-1" || o.ContainerID() != "TOTE-1" || o.Destination() != station || o.Priority() != tr.PriorityNormal ||
		!o.DueBy().Equal(r.DueBy) || o.Version() != 0 {
		t.Fatalf("accessors = %s %s %+v %s %s %d", o.RequestID(), o.ContainerID(), o.Destination(), o.Priority(), o.DueBy(), o.Version())
	}
	if _, ok := o.Previous("x"); ok {
		t.Fatal("Previous of an unknown reference found a leg")
	}
	if _, ok := o.LegByRef("x"); ok {
		t.Fatal("LegByRef of an unknown reference found a leg")
	}
	for name, err := range map[string]error{
		"Requested": o.Requested("x", "j", t0), "ResolveTo": o.ResolveTo("x", in1), "PickedUp": o.PickedUp("x", "R", t0),
		"Presented": o.Presented("x", out1, t0), "Replan": o.Replan("x", pickPlan(), t0),
	} {
		if err == nil {
			t.Fatalf("%s(unknown reference) = nil, want a refusal", name)
		}
	}
	if _, err := o.RetryLeg("x", t0); err == nil {
		t.Fatal("RetryLeg(unknown reference) = nil, want a refusal")
	}
	assertRequestedKeepsAPlannedLegAndEndedOrdersRefuse(t, o)
}

// Split from TestTheAccessorsAndTheRefusalsOfUnknownLegsAndEndedOrders for cyclop (max 15).
func assertRequestedKeepsAPlannedLegAndEndedOrdersRefuse(t *testing.T, o *to.Order) {
	t.Helper()
	retrieve, _ := o.Next(t0)
	// Requested never moves a leg back or on (a fact may have come first): on the PLANNED second leg it keeps the
	// status and records the first id only.
	second := o.Legs()[1].Ref
	must(t, o.Requested(second, "j-1", t0))
	must(t, o.Requested(second, "j-2", t0))
	if l, _ := o.LegByRef(second); l.Status != to.LegPlanned || l.SubsystemRef != "j-1" {
		t.Fatalf("Requested on the PLANNED second leg = %s %q, want PLANNED with the first id", l.Status, l.SubsystemRef)
	}
	must(t, o.Fail(to.FailCancelled, "", "", t0))
	if cur, ok := o.Current(); ok {
		t.Fatalf("Current after Fail = %+v, want none", cur)
	}
	if _, act := o.Next(t0); act != to.ActionNone {
		t.Fatalf("Next after Fail = %s, want NONE", act)
	}
	if _, err := o.RetryLeg(retrieve.Ref, t0); !errors.Is(err, to.ErrEnded) && !errors.Is(err, to.ErrNotRetryable) {
		t.Fatalf("RetryLeg after Fail = %v", err)
	}
	if err := o.Replan(retrieve.Ref, []routing.LegPlan{}, t0); err == nil {
		t.Fatal("Replan after Fail = nil, want a refusal")
	}
}
