package usecases_test

import (
	"testing"
	"time"

	"github.com/IQVO/material-flow-control/internal/application/command"
	ct "github.com/IQVO/material-flow-control/internal/domain/containertrack"
	"github.com/IQVO/material-flow-control/internal/domain/transportorder"
	tr "github.com/IQVO/material-flow-control/internal/domain/transportrequest"
)

// timedOut accepts ST1 -> pack, requests its MOVE, then lets Tick end it TIMEOUT and sends the best-effort cancel.
func (e *env) timedOut() string {
	e.t.Helper()
	v := e.mustAccept("rq-1", "TOTE-1", "ST1", pack, tr.PurposeOutbound)
	e.dispatch()
	e.clock.Advance(900 * time.Second)
	if n, err := e.svc.Tick(ctx); n != 1 || err != nil {
		e.t.Fatalf("Tick = %d %v", n, err)
	}
	e.dispatch()
	if c := e.amr.Calls(); c[len(c)-1].Op != "CancelLeg" {
		e.t.Fatalf("calls = %+v", c)
	}
	return v.OrderID + ".1.1"
}

// carry (Task 10 review): D16 - the track follows physical facts even for an ended order. Tick ended the transport
// TIMEOUT; the best-effort CANCEL_LEG then answered LegFailed CANCELLED with the container on the robot. The track
// moves onto the robot and is held CONTAINER_ON_FAILED_ROBOT with one ContainerSentToClarification; the order
// stays FAILED TIMEOUT. A repeated fact is a no-op (ruling P4-R5: a second hold keeps the first, no event).
func TestALegFailedOnTheRobotAfterTheTimeoutMovesAndHoldsTheContainer(t *testing.T) {
	e := newEnv(t)
	ref := e.timedOut()
	before := len(e.events.All())
	e.legFailed(ref, "TOTE-1", "CANCELLED", true)
	if p := e.position("TOTE-1"); p != ct.OnRobotOf("AMR1", "EMU/R1") {
		t.Fatalf("track = %+v, want on AMR1/EMU/R1", p)
	}
	track, err := e.tracks.Get(ctx, "TOTE-1")
	e.must(err)
	c, held := track.Clarification()
	if !held || c.Reason != ct.ReasonOnFailedRobot {
		t.Fatalf("track = %+v, want held %s", track.Snapshot(), ct.ReasonOnFailedRobot)
	}
	evs := e.events.All()[before:]
	if len(evs) != 1 {
		t.Fatalf("events = %+v, want one ContainerSentToClarification", evs)
	}
	if sent, ok := evs[0].(ct.ContainerSentToClarification); !ok || sent.Reason != ct.ReasonOnFailedRobot || sent.ContainerID != "TOTE-1" || sent.DetectedAt != "ST1" {
		t.Fatalf("event = %+v", evs[0])
	}
	o, err := e.svc.OrderDetail(ctx, "rq-1")
	e.must(err)
	if o.Status != transportorder.StatusFailed || o.Reason != transportorder.FailTimeout {
		t.Fatalf("order = %s %s, want FAILED TIMEOUT", o.Status, o.Reason)
	}
	e.clock.Advance(time.Hour)
	e.legFailed(ref, "TOTE-1", "CANCELLED", true)
	again, err := e.tracks.Get(ctx, "TOTE-1")
	e.must(err)
	if c2, _ := again.Clarification(); c2 != c || len(e.events.All()) != before+1 {
		t.Fatalf("a repeated fact changed the hold or published: %+v, events %d", c2, len(e.events.All())-before)
	}
	if p := e.pending(command.RequestLeg); len(p) != 0 {
		t.Fatalf("a leg was planned for an ended order: %+v", p)
	}
}

// carry (Task 10 review): with the container NOT on the robot, a late LegFailed for an ended order changes nothing.
func TestALegFailedOffTheRobotAfterTheTimeoutChangesNothing(t *testing.T) {
	e := newEnv(t)
	ref := e.timedOut()
	before := len(e.events.All())
	e.legFailed(ref, "TOTE-1", "CANCELLED", false)
	if p := e.position("TOTE-1"); p != (ct.Position{Kind: ct.AtStation, Code: "ST1"}) {
		t.Fatalf("track = %+v, want at ST1", p)
	}
	track, err := e.tracks.Get(ctx, "TOTE-1")
	e.must(err)
	if _, held := track.Clarification(); held || len(e.events.All()) != before {
		t.Fatalf("track = %+v, events %+v", track.Snapshot(), e.events.All()[before:])
	}
}

// carry (Task 11 review): the ended order's container was re-requested (rq-2 accepted at ST1) before the late
// LegFailed CANCELLED with the container on the robot arrived. The container is held and on AMR1, so rq-2 can
// never run (responsibility 4, D12): it fails CLARIFICATION at once, no leg is requested for it, and the track
// is held with no active order.
func TestALateOnRobotFailureFailsTheReRequestOfTheNowHeldContainer(t *testing.T) {
	e := newEnv(t)
	refA := e.timedOut()
	vb := e.mustAccept("rq-2", "TOTE-1", "ST1", pack, tr.PurposeOutbound)
	before, calls := len(e.events.All()), len(e.amr.Calls())
	e.legFailed(refA, "TOTE-1", "CANCELLED", true)
	evs := e.events.All()[before:]
	if len(evs) != 2 {
		t.Fatalf("events = %+v, want the hold then rq-2 failed", evs)
	}
	if sent, ok := evs[0].(ct.ContainerSentToClarification); !ok || sent.Reason != ct.ReasonOnFailedRobot {
		t.Fatalf("first event = %+v", evs[0])
	}
	if f, ok := evs[1].(transportorder.TransportFailed); !ok || f.RequestID != "rq-2" || f.OrderID != vb.OrderID ||
		f.Reason != transportorder.FailClarification || f.SubsystemReason != string(ct.ReasonOnFailedRobot) || f.ContainerLocation != "" {
		t.Fatalf("second event = %+v", evs[1])
	}
	e.dispatch()
	for _, c := range e.amr.Calls()[calls:] {
		if c.Op == "RequestLeg" {
			t.Fatalf("a leg was requested for a held container: %+v", c)
		}
	}
	track, err := e.tracks.Get(ctx, "TOTE-1")
	e.must(err)
	if _, held := track.Clarification(); !held || track.ActiveOrderID() != "" || track.Position() != ct.OnRobotOf("AMR1", "EMU/R1") {
		t.Fatalf("track = %+v", track.Snapshot())
	}
}

// carry (Task 11 review): same, with rq-2's MOVE already requested at the AMR: it is cancelled best-effort.
func TestALateOnRobotFailureCancelsTheReRequestsLegInFlight(t *testing.T) {
	e := newEnv(t)
	refA := e.timedOut()
	e.mustAccept("rq-2", "TOTE-1", "ST1", pack, tr.PurposeOutbound)
	e.dispatch()
	inFlight, err := e.svc.OrderDetail(ctx, "rq-2")
	e.must(err)
	refB := inFlight.Legs[0].Ref
	e.legFailed(refA, "TOTE-1", "CANCELLED", true)
	e.dispatch()
	c := e.amr.Calls()
	if last := c[len(c)-1]; last.Op != "CancelLeg" || last.Key != "cancel-"+refB {
		t.Fatalf("calls = %+v", c)
	}
	o, err := e.svc.OrderDetail(ctx, "rq-2")
	e.must(err)
	if o.Status != transportorder.StatusFailed || o.Reason != transportorder.FailClarification {
		t.Fatalf("rq-2 = %s %s", o.Status, o.Reason)
	}
}
