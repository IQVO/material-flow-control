package usecases_test

import (
	"testing"

	"github.com/IQVO/material-flow-control/internal/application/command"
	"github.com/IQVO/material-flow-control/internal/application/facts"
	"github.com/IQVO/material-flow-control/internal/domain/transportorder"
	tr "github.com/IQVO/material-flow-control/internal/domain/transportrequest"
)

// unchanged fails when rq-1 is no longer IN_PROGRESS with the legs it had, or a retry was counted.
func (e *env) unchanged(before transportorder.Snapshot) {
	e.t.Helper()
	o, err := e.svc.OrderDetail(ctx, "rq-1")
	e.must(err)
	if o.Status != transportorder.StatusInProgress || o.Retries != 0 || o.Replans != 0 || len(o.Legs) != len(before.Legs) || o.Legs[0] != before.Legs[0] {
		e.t.Fatalf("order = %s %s, retries %d, replans %d, legs %+v; want IN_PROGRESS, no retry, legs %+v", o.Status, o.Reason, o.Retries,
			o.Replans, o.Legs, before.Legs)
	}
}

// carry (Task 10 review): amr-fleet-control's LegFailed ends only a MOVE. A LegFailed naming a RETRIEVE's
// reference is skipped: the order stays IN_PROGRESS, the leg unchanged, no retry counted.
func TestALegFailedForARetrieveIsRefusedAndTheOrderRuns(t *testing.T) {
	e := newEnv(t)
	e.stored("TOTE-1")
	e.mustAccept("rq-1", "TOTE-1", asrsLoc, "ST1", tr.PurposePick)
	retrieve := e.lastRef(command.RequestLeg)
	e.done()
	before, err := e.svc.OrderDetail(ctx, "rq-1")
	e.must(err)
	e.legFailed(retrieve, "TOTE-1", "ROBOT_BLOCKED", false)
	e.unchanged(before)
	if p := e.pending(command.RequestLeg); len(p) != 0 {
		t.Fatalf("a leg was requested for a refused failure: %+v", p)
	}
}

// carry (Task 10 review): asrs-control's StorageJobFailed ends only a RETRIEVE or a STORE. One naming a MOVE's
// reference is skipped: the order stays IN_PROGRESS with the MOVE unchanged.
func TestAStorageJobFailedForAMoveIsRefusedAndTheOrderRuns(t *testing.T) {
	e := newEnv(t)
	e.mustAccept("rq-1", "TOTE-1", "ST1", pack, tr.PurposeOutbound)
	move := e.lastRef(command.RequestLeg)
	e.done()
	before, err := e.svc.OrderDetail(ctx, "rq-1")
	e.must(err)
	e.must(e.svc.OnStorageJobFailed(ctx, e.id(), typeJobFailed, facts.StorageJobFailed{AsrsID: "ASRS1", Kind: "STORE", ContainerID: "TOTE-1",
		PortID: "IN1", Reference: move, Reason: "EQUIPMENT_FAULT"}))
	e.unchanged(before)
}

// carry (Task 10 review): only a MOVE's pickup from an output port is confirmed to asrs-control. A ContainerPickedUp
// naming a RETRIEVE's reference enqueues no CONFIRM_PICKUP and leaves the RETRIEVE unchanged.
func TestAPickupNamingARetrieveConfirmsNothing(t *testing.T) {
	e := newEnv(t)
	e.stored("TOTE-1")
	e.mustAccept("rq-1", "TOTE-1", asrsLoc, "ST1", tr.PurposePick)
	retrieve := e.lastRef(command.RequestLeg)
	e.done()
	before, err := e.svc.OrderDetail(ctx, "rq-1")
	e.must(err)
	e.pickedUp(retrieve, "TOTE-1", "ASRS1-OUT1")
	if c := e.pending(command.ConfirmPickup); len(c) != 0 {
		t.Fatalf("pickup confirmations = %+v, want none", c)
	}
	e.unchanged(before)
}

// carry (Task 10 review), decision D16: the pickup of a MOVE from an output port is still confirmed when its order
// already ended (Tick failed it TIMEOUT), so asrs-control frees the port the robot just emptied.
func TestAPickupOfAnEndedOrdersMoveFromAnOutputPortIsStillConfirmed(t *testing.T) {
	e := newEnv(t)
	move := e.timedOutAtOut1()
	e.pickedUp(move, "TOTE-1", "ASRS1-OUT1")
	if c := e.pending(command.ConfirmPickup); len(c) != 1 || c[0].LegRef != move {
		t.Fatalf("pickup confirmations = %+v, want one for %s", c, move)
	}
}
