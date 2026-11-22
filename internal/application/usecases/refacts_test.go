package usecases_test

import (
	"testing"

	"github.com/IQVO/material-flow-control/internal/application/command"
	ct "github.com/IQVO/material-flow-control/internal/domain/containertrack"
)

// carry (Task 10 review): a ContainerPresentedAtPort re-emitted under a NEW event id after the robot picked the
// container up is stale: the track stays on the robot (the presentation already ended its RETRIEVE) and nothing
// new is dispatched.
func TestARePresentationAfterThePickupLeavesTheContainerOnTheRobot(t *testing.T) {
	e := newEnv(t)
	e.pickedUpAtOut1()
	o, _ := e.svc.OrderDetail(ctx, "rq-1")
	legs := len(e.pending(command.RequestLeg))
	e.presented(o.Legs[0].Ref, "TOTE-1", "OUT1", "READ")
	if p := e.position("TOTE-1"); p.Kind != ct.OnRobot {
		t.Fatalf("position after a stale presentation = %+v, want still on the robot", p)
	}
	if n := len(e.pending(command.RequestLeg)); n != legs {
		t.Fatalf("pending REQUEST_LEG %d after a stale presentation, want %d", n, legs)
	}
}

// carry (Task 10 review): a ContainerPickedUp re-emitted under a NEW event id confirms the port pickup once.
func TestARepeatedPickupConfirmsTheOutputPortOnce(t *testing.T) {
	e := newEnv(t)
	e.pickedUpAtOut1()
	e.pickedUp(e.lastRef(command.RequestLeg), "TOTE-1", "ASRS1-OUT1")
	if c := e.pending(command.ConfirmPickup); len(c) != 1 {
		t.Fatalf("CONFIRM_PICKUP commands after a repeated pickup = %+v, want exactly one", c)
	}
}
