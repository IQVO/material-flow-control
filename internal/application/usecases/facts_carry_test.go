package usecases_test

import (
	"testing"

	"github.com/IQVO/material-flow-control/internal/application/command"
	"github.com/IQVO/material-flow-control/internal/application/facts"
	"github.com/IQVO/material-flow-control/internal/application/usecases"
	ct "github.com/IQVO/material-flow-control/internal/domain/containertrack"
	"github.com/IQVO/material-flow-control/internal/domain/routing"
	"github.com/IQVO/material-flow-control/internal/domain/transportorder"
	tr "github.com/IQVO/material-flow-control/internal/domain/transportrequest"
)

// carry (Task 3 review): the re-plan-once bound (decision D13) is the use case's `o.Replans() < 1` guard, not
// routing: StoreViaOtherInput has no memory and would route the failed IN2 store back through IN1. A second
// StorageJobFailed after the one re-plan fails the transport SUBSYSTEM_FAULT and plans no third STORE.
func TestASecondFailedStoreAfterTheReplanFailsAndPlansNoThirdStore(t *testing.T) {
	e := newEnv(t)
	e.mustAccept("rq-1", "TOTE-1", decant, asrsLoc, tr.PurposePutaway)
	move := e.lastRef(command.RequestLeg)
	e.done()
	e.completed(move, "TOTE-1", "ASRS1-IN1")
	fail := func(ref string) {
		e.must(e.svc.OnStorageJobFailed(ctx, e.id(), typeJobFailed, facts.StorageJobFailed{AsrsID: "ASRS1", Kind: "STORE", ContainerID: "TOTE-1",
			PortID: "IN1", Reference: ref, Reason: "PORT_OCCUPIED"}))
	}
	fail(e.lastRef(command.RequestLeg))
	replan := e.lastRef(command.RequestLeg)
	e.done()
	e.completed(replan, "TOTE-1", "ASRS1-IN2")
	second := e.lastRef(command.RequestLeg)
	e.done()
	fail(second)
	if p := e.pending(command.RequestLeg); len(p) != 0 {
		t.Fatalf("a leg was planned after the second failed store: %+v", p)
	}
	o, err := e.svc.OrderDetail(ctx, "rq-1")
	e.must(err)
	stores := 0
	for _, l := range o.Legs {
		if l.Kind == routing.KindStore {
			stores++
		}
	}
	if stores != 2 || o.Replans != 1 || o.Status != transportorder.StatusFailed || o.Reason != transportorder.FailSubsystemFault {
		t.Fatalf("stores = %d, replans = %d, status %s %s; want 2 stores, 1 re-plan, FAILED SUBSYSTEM_FAULT", stores, o.Replans, o.Status, o.Reason)
	}
	if f, ok := e.last().(transportorder.TransportFailed); !ok || f.SubsystemReason != "PORT_OCCUPIED" {
		t.Fatalf("last = %+v", e.last())
	}
}

// carry (plan 03 Task 9 review): amr-fleet-control may answer a requested cancellation with LegFailed CANCELLED
// and container_on_robot true (the robot picked before the cancel reached it). The order still ends CANCELLED,
// and the track follows the container onto the robot; with container_on_robot false it stays at the source.
// carry (Task 10 review): on the robot the container is also held CONTAINER_ON_FAILED_ROBOT (decision D12) with a
// ContainerSentToClarification before the TransportFailed, so an operator can resolve it and it can be requested
// again; with container_on_robot false nothing is held.
func TestACancelledLegWithTheContainerOnTheRobotEndsCancelledWithTheTrackOnTheRobot(t *testing.T) {
	for _, onRobot := range []bool{true, false} {
		e := newEnv(t)
		e.mustAccept("rq-1", "TOTE-1", "ST1", pack, tr.PurposeOutbound)
		ref := e.lastRef(command.RequestLeg)
		if _, out, err := e.svc.CancelTransport(ctx, "rq-1"); err != nil || out != usecases.CancelAsked {
			t.Fatalf("cancel = %s %v", out, err)
		}
		before := len(e.events.All())
		e.legFailed(ref, "TOTE-1", "CANCELLED", onRobot)
		if f, ok := e.last().(transportorder.TransportFailed); !ok || f.Reason != transportorder.FailCancelled {
			t.Fatalf("on robot %v: last = %+v", onRobot, e.last())
		}
		want := ct.Position{Kind: ct.AtStation, Code: "ST1"}
		if onRobot {
			want = ct.OnRobotOf("AMR1", "EMU/R1")
		}
		if p := e.position("TOTE-1"); p != want {
			t.Fatalf("on robot %v: track = %+v, want %+v", onRobot, p, want)
		}
		if onRobot {
			assertTheCancelledContainerOnTheRobotIsHeldAndResolvable(t, e, e.events.All()[before:])
		} else {
			assertTheCancelledContainerAtTheSourceIsNotHeld(t, e, e.events.All()[before:])
		}
	}
}

func assertTheCancelledContainerOnTheRobotIsHeldAndResolvable(t *testing.T, e *env, evs []any) {
	t.Helper()
	track, err := e.tracks.Get(ctx, "TOTE-1")
	e.must(err)
	if c, held := track.Clarification(); !held || c.Reason != ct.ReasonOnFailedRobot {
		t.Fatalf("on robot: track = %+v, want held %s", track.Snapshot(), ct.ReasonOnFailedRobot)
	}
	if len(evs) != 2 {
		t.Fatalf("on robot: events = %+v, want ContainerSentToClarification then TransportFailed", evs)
	}
	sent, ok := evs[0].(ct.ContainerSentToClarification)
	if !ok || sent.Reason != ct.ReasonOnFailedRobot || sent.DetectedAt != "ST1" || sent.ContainerID != "TOTE-1" {
		t.Fatalf("on robot: first event = %+v", evs[0])
	}
	if f, ok := evs[1].(transportorder.TransportFailed); !ok || f.Reason != transportorder.FailCancelled || f.ContainerLocation != "" {
		t.Fatalf("on robot: second event = %+v, want TransportFailed CANCELLED at \"\"", evs[1])
	}
	if _, err := e.svc.ResolveClarification(ctx, "TOTE-1", "ST1"); err != nil {
		t.Fatalf("on robot: resolve = %v", err)
	}
	if _, err := e.request("rq-2", "TOTE-1", "ST1", pack, tr.PurposeOutbound); err != nil {
		t.Fatalf("on robot: re-request after the resolve = %v", err)
	}
}

func assertTheCancelledContainerAtTheSourceIsNotHeld(t *testing.T, e *env, evs []any) {
	t.Helper()
	track, err := e.tracks.Get(ctx, "TOTE-1")
	e.must(err)
	if _, held := track.Clarification(); held {
		t.Fatalf("at the source: track held = %+v", track.Snapshot())
	}
	for _, ev := range evs {
		if _, sent := ev.(ct.ContainerSentToClarification); sent {
			t.Fatalf("at the source: a ContainerSentToClarification was published: %+v", evs)
		}
	}
}
