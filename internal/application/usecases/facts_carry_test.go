package usecases_test

import (
	"testing"

	"github.com/IQVO/material-flow-control/internal/application/command"
	"github.com/IQVO/material-flow-control/internal/application/facts"
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
