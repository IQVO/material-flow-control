package usecases_test

import (
	"testing"
	"time"

	"github.com/IQVO/material-flow-control/internal/application/command"
	"github.com/IQVO/material-flow-control/internal/application/facts"
	"github.com/IQVO/material-flow-control/internal/application/usecases"
	ct "github.com/IQVO/material-flow-control/internal/domain/containertrack"
	"github.com/IQVO/material-flow-control/internal/domain/transportorder"
	tr "github.com/IQVO/material-flow-control/internal/domain/transportrequest"
)

// timedOutAtOut1 picks TOTE-1 from the AS/RS to ST1, presents it at OUT1 (READ), requests its MOVE and lets Tick
// end the transport TIMEOUT before any pickup: the container still stands on OUT1, not held. It returns the
// MOVE's reference.
func (e *env) timedOutAtOut1() string {
	e.t.Helper()
	e.stored("TOTE-1")
	e.mustAccept("rq-1", "TOTE-1", asrsLoc, "ST1", tr.PurposePick)
	e.dispatch()
	o, err := e.svc.OrderDetail(ctx, "rq-1")
	e.must(err)
	e.presented(o.Legs[0].Ref, "TOTE-1", "OUT1", "READ")
	move := e.lastRef(command.RequestLeg)
	e.dispatch()
	e.clock.Advance(900 * time.Second)
	if n, err := e.svc.Tick(ctx); n != 1 || err != nil {
		e.t.Fatalf("Tick = %d %v", n, err)
	}
	return move
}

// openInternal is the open internal (clarification) order of the container, or nil.
func (e *env) openInternal(container string) *transportorder.Order {
	e.t.Helper()
	open, err := e.orders.Open(ctx)
	e.must(err)
	for _, o := range open {
		if o.Internal() && o.ContainerID() == container {
			return o
		}
	}
	return nil
}

// carry (Task 11 review): a robot-less DROP_FAILED for an ended order whose container still stands on an AS/RS
// output port holds it and builds the internal clarification order (decision D12). failReRequest must read the
// re-request BEFORE clarify: reading the track afterwards finds the internal order clarify just reserved and
// fails it, so the exception event names a clarification move that never runs.
func TestALateDropFailureAtAnOutputPortKeepsTheClarificationMoveLive(t *testing.T) {
	e := newEnv(t)
	move := e.timedOutAtOut1()
	before := len(e.events.All())
	e.must(e.svc.OnLegFailed(ctx, e.id(), typeLegFailed, facts.LegFailed{LegID: "leg", FleetID: "AMR1", ContainerID: "TOTE-1",
		Reference: move, Reason: "DROP_FAILED"}))
	io := e.openInternal("TOTE-1")
	if io == nil || io.Ended() {
		t.Fatalf("the internal clarification order was failed: open %+v", io)
	}
	var live bool
	for _, c := range e.pending(command.RequestLeg) {
		live = live || c.OrderID == io.ID()
	}
	if !live {
		t.Fatalf("no live REQUEST_LEG for %s: %+v", io.ID(), e.pending(command.RequestLeg))
	}
	var sent []ct.ContainerSentToClarification
	for _, ev := range e.events.All()[before:] {
		if s, ok := ev.(ct.ContainerSentToClarification); ok {
			sent = append(sent, s)
		}
		if f, ok := ev.(transportorder.TransportFailed); ok {
			t.Fatalf("an order failed: %+v", f)
		}
	}
	if len(sent) != 1 || sent[0].ClarificationPoint != clrLoc || io.Destination().Code != clrLoc {
		t.Fatalf("events = %+v, internal to %+v, want one ContainerSentToClarification to %s", sent, io.Destination(), clrLoc)
	}
	track, err := e.tracks.Get(ctx, "TOTE-1")
	e.must(err)
	if c, held := track.Clarification(); !held || c.Reason != ct.ReasonOnFailedRobot || track.ActiveOrderID() != io.ID() {
		t.Fatalf("track = %+v", track.Snapshot())
	}
}

// carry (Task 11 review): an ended INTERNAL order has no re-request to fail and its container is already held
// (P4-R5): a late on-robot failure only moves the track onto the robot (D16); the hold, the events and every
// order are unchanged.
func TestALateOnRobotFailureOfAnEndedClarificationMoveOnlyMovesTheTrack(t *testing.T) {
	e := newEnv(t)
	e.stored("TOTE-1")
	e.mustAccept("rq-1", "TOTE-1", asrsLoc, "ST1", tr.PurposePick)
	e.dispatch()
	o, err := e.svc.OrderDetail(ctx, "rq-1")
	e.must(err)
	e.presented(o.Legs[0].Ref, "TOTE-1", "OUT1", "NO_READ")
	io := e.openInternal("TOTE-1")
	if io == nil {
		t.Fatal("no internal clarification order")
	}
	e.dispatch()
	e.clock.Advance(900 * time.Second)
	if n, err := e.svc.Tick(ctx); n != 1 || err != nil {
		t.Fatalf("Tick = %d %v", n, err)
	}
	held, err := e.tracks.Get(ctx, "TOTE-1")
	e.must(err)
	first, _ := held.Clarification()
	before := len(e.events.All())
	e.legFailed(io.ID()+".1.1", "TOTE-1", "DROP_FAILED", true)
	track, err := e.tracks.Get(ctx, "TOTE-1")
	e.must(err)
	if c, ok := track.Clarification(); !ok || c != first || track.Position() != ct.OnRobotOf("AMR1", "EMU/R1") {
		t.Fatalf("track = %+v, want hold %+v on AMR1/EMU/R1", track.Snapshot(), first)
	}
	if evs := e.events.All()[before:]; len(evs) != 0 {
		t.Fatalf("events = %+v, want none", evs)
	}
}

// carry (Task 12 review): ResolveClarification can lift the hold after an internal clarification order ended, and a
// re-request can then reserve the container. A late on-robot failure of the ENDED internal order must hold the
// container again (CONTAINER_ON_FAILED_ROBOT, decision D12) and fail the re-request (responsibility 4).
func TestALateOnRobotFailureOfAnEndedClarificationMoveAfterAResolveHoldsAgain(t *testing.T) {
	e := newEnv(t)
	io := e.resolvedAfterATimedOutClarificationMove()
	e.mustAccept("rq-2", "TOTE-1", clrLoc, asrsLoc, tr.PurposeReturn)
	before := len(e.events.All())
	e.legFailed(io.ID()+".1.1", "TOTE-1", "DROP_FAILED", true)
	track, err := e.tracks.Get(ctx, "TOTE-1")
	e.must(err)
	if c, held := track.Clarification(); !held || c.Reason != ct.ReasonOnFailedRobot || c.OrderID != io.ID() || c.DetectedAt != out1Loc {
		t.Fatalf("track = %+v, want held CONTAINER_ON_FAILED_ROBOT for %s at %s", track.Snapshot(), io.ID(), out1Loc)
	}
	if v, err := e.svc.Transport(ctx, "rq-2"); err != nil || v.Status != usecases.ViewFailed || v.Reason != string(transportorder.FailClarification) {
		t.Fatalf("rq-2 = %+v %v, want FAILED CLARIFICATION", v, err)
	}
	if sent, failed := countHoldAndFail(e.events.All()[before:]); sent != 1 || failed != 1 {
		t.Fatalf("events = %+v, want one ContainerSentToClarification and one TransportFailed", e.events.All()[before:])
	}
}

// resolvedAfterATimedOutClarificationMove opens the internal clarification order of TOTE-1 with a NO_READ at OUT1,
// lets Tick fail it TIMEOUT after dispatch and resolves the hold at the clarification point. It returns the order.
func (e *env) resolvedAfterATimedOutClarificationMove() *transportorder.Order {
	e.t.Helper()
	e.stored("TOTE-1")
	e.mustAccept("rq-1", "TOTE-1", asrsLoc, "ST1", tr.PurposePick)
	e.dispatch()
	o, err := e.svc.OrderDetail(ctx, "rq-1")
	e.must(err)
	e.presented(o.Legs[0].Ref, "TOTE-1", "OUT1", "NO_READ")
	io := e.openInternal("TOTE-1")
	if io == nil {
		e.t.Fatal("no internal clarification order")
	}
	e.dispatch()
	e.clock.Advance(900 * time.Second)
	if n, err := e.svc.Tick(ctx); n != 1 || err != nil {
		e.t.Fatalf("Tick = %d %v", n, err)
	}
	_, err = e.svc.ResolveClarification(ctx, "TOTE-1", clrLoc)
	e.must(err)
	return io
}

// countHoldAndFail counts the ContainerSentToClarification and TransportFailed events.
func countHoldAndFail(evs []any) (sent, failed int) {
	for _, ev := range evs {
		switch ev.(type) {
		case ct.ContainerSentToClarification:
			sent++
		case transportorder.TransportFailed:
			failed++
		}
	}
	return sent, failed
}
