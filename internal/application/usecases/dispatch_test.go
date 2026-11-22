package usecases_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/IQVO/material-flow-control/internal/application/command"
	"github.com/IQVO/material-flow-control/internal/application/subsystem"
	"github.com/IQVO/material-flow-control/internal/application/usecases"
	"github.com/IQVO/material-flow-control/internal/domain/transportorder"
	tr "github.com/IQVO/material-flow-control/internal/domain/transportrequest"
)

func (e *env) dispatch() int {
	e.t.Helper()
	n, err := e.svc.DispatchDue(ctx)
	if err != nil {
		e.t.Fatal(err)
	}
	return n
}

func TestARetrieveIsRequestedWithOurReferenceAndKeyAndRecordsTheJob(t *testing.T) {
	e := newEnv(t)
	e.stored("TOTE-1")
	v := e.mustAccept("rq-1", "TOTE-1", asrsLoc, "ST1", tr.PurposePick)
	if n := e.dispatch(); n != 1 {
		t.Fatalf("dispatched %d", n)
	}
	calls := e.asrs.Calls()
	ref := v.OrderID + ".1.1"
	if len(calls) != 1 || calls[0].Op != "CreateStorageJob" || calls[0].Key != ref || calls[0].SubsystemID != "ASRS1" ||
		calls[0].Storage != (subsystem.StorageJobRequest{Kind: "RETRIEVE", ContainerID: "TOTE-1", Reference: ref}) {
		t.Fatalf("calls = %+v", calls)
	}
	o, _ := e.svc.OrderDetail(ctx, "rq-1")
	if o.Legs[0].Status != transportorder.LegRequested || o.Legs[0].SubsystemRef != "sj-1" {
		t.Fatalf("leg = %+v", o.Legs[0])
	}
	if e.dispatch() != 0 {
		t.Fatal("a done command was dispatched again")
	}
}

func TestAMoveIntoTheAsrsTakesTheRecommendedInputPortAndTheStoreUsesIt(t *testing.T) {
	e := newEnv(t)
	e.asrs.Recommended = "IN2"
	v := e.mustAccept("rq-1", "TOTE-1", decant, asrsLoc, tr.PurposePutaway)
	e.dispatch()
	move := e.amr.Calls()
	if len(move) != 1 || move[0].Leg != (subsystem.LegRequest{ContainerID: "TOTE-1", From: "ST2", To: "ASRS1-IN2", Reference: v.OrderID + ".1.1"}) {
		t.Fatalf("move = %+v", move)
	}
	e.completed(v.OrderID+".1.1", "TOTE-1", "ASRS1-IN2")
	e.dispatch()
	calls := e.asrs.Calls()
	store := calls[len(calls)-1]
	if store.Op != "CreateStorageJob" || store.Storage.Kind != "STORE" || store.Storage.PortID != "IN2" {
		t.Fatalf("store = %+v", store)
	}

	f := newEnv(t)
	f.asrs.Recommended = ""
	f.mustAccept("rq-1", "TOTE-1", decant, asrsLoc, tr.PurposePutaway)
	f.dispatch()
	if got := f.amr.Calls()[0].Leg.To; got != "ASRS1-IN1" {
		t.Fatalf("without a recommendation the first input port: %s", got)
	}
}

func TestARetryableRefusalIsRetriedUnderTheSameKeyAndAFinalOneFailsAtOnce(t *testing.T) {
	e := newEnv(t)
	e.mustAccept("rq-1", "TOTE-1", "ST1", pack, tr.PurposeOutbound)
	e.amr.Answer(&subsystem.Refusal{Status: 422, Code: "fleet-unavailable", Retryable: true})
	e.dispatch()
	c := e.pending(command.RequestLeg)
	if len(c) != 1 || c[0].Attempts != 1 || !c[0].NextAttemptAt.Equal(t0.Add(time.Second)) || c[0].LastError == "" {
		t.Fatalf("after a retryable refusal = %+v", c)
	}
	if e.dispatch() != 0 {
		t.Fatal("dispatched before the backoff")
	}
	e.clock.Advance(time.Second)
	e.dispatch()
	calls := e.amr.Calls()
	if len(calls) != 2 || calls[0].Key != calls[1].Key || calls[0].Leg != calls[1].Leg {
		t.Fatalf("the retry changed the key or the body: %+v", calls)
	}
	if v, _ := e.svc.Transport(ctx, "rq-1"); v.Status != usecases.ViewInProgress {
		t.Fatalf("view = %+v", v)
	}

	g := newEnv(t)
	g.mustAccept("rq-1", "TOTE-1", "ST1", pack, tr.PurposeOutbound)
	// carry (Task 20 review): bad-request is FINAL in both producer contracts; unknown-handoff-point is RETRYABLE since
	// plan 03 ruling P3-R13, so the FINAL path is driven with bad-request.
	g.amr.Answer(&subsystem.Refusal{Status: 400, Code: "bad-request"})
	g.dispatch()
	if f, _ := g.last().(transportorder.TransportFailed); f.Reason != transportorder.FailSubsystemFault || f.SubsystemReason != "bad-request" {
		t.Fatalf("final refusal = %+v", g.last())
	}
	if all := g.cmds.All(); all[0].Status != command.StatusFailed {
		t.Fatalf("command = %+v", all[0])
	}
}

func TestAnUnreachableSubsystemFailsTheTransportAfterTheDeadline(t *testing.T) {
	e := newEnv(t)
	e.mustAccept("rq-1", "TOTE-1", "ST1", pack, tr.PurposeOutbound)
	for i := 0; i < 20; i++ {
		e.amr.Answer(fmt.Errorf("dial: %w", subsystem.ErrUnreachable))
	}
	e.dispatch()
	e.clock.Advance(301 * time.Second)
	e.dispatch()
	if f, _ := e.last().(transportorder.TransportFailed); f.Reason != transportorder.FailSubsystemFault || f.SubsystemReason != "subsystem-unreachable" {
		t.Fatalf("after the deadline = %+v", e.last())
	}
}

func TestCancellationIsSentWithTheSubsystemIDAndARefusalLetsTheTransportRun(t *testing.T) {
	e := newEnv(t)
	v := e.mustAccept("rq-1", "TOTE-1", "ST1", pack, tr.PurposeOutbound)
	if _, _, err := e.svc.CancelTransport(ctx, "rq-1"); err != nil {
		t.Fatal(err)
	}
	e.amr.Answer(nil, &subsystem.Refusal{Status: 409, Code: "leg-not-cancellable", Retryable: true})
	if n := e.dispatch(); n != 2 {
		t.Fatalf("dispatched %d (the request, then the cancel in Seq order)", n)
	}
	calls := e.amr.Calls()
	if len(calls) != 2 || calls[1].Op != "CancelLeg" || calls[1].Key != "cancel-"+v.OrderID+".1.1" || calls[1].TargetID != "leg-1" {
		t.Fatalf("calls = %+v", calls)
	}
	if got, _ := e.svc.Transport(ctx, "rq-1"); got.Status != usecases.ViewInProgress {
		t.Fatalf("a refused cancellation lets the transport run: %+v", got)
	}
}

func TestAPickupIsConfirmedToTheAsrsAndAnObsoleteRequestIsNotSent(t *testing.T) {
	e := newEnv(t)
	e.stored("TOTE-1")
	e.mustAccept("rq-1", "TOTE-1", asrsLoc, "ST1", tr.PurposePick)
	e.dispatch()
	o, _ := e.svc.OrderDetail(ctx, "rq-1")
	e.presented(o.Legs[0].Ref, "TOTE-1", "OUT1", "READ")
	move := e.lastRef(command.RequestLeg)
	e.pickedUp(move, "TOTE-1", "ASRS1-OUT1")
	e.dispatch()
	var pickup, moves int
	for _, c := range e.asrs.Calls() {
		if c.Op == "ConfirmPickup" && c.Key == "pickup-"+move && c.PortID == "OUT1" && c.ContainerID == "TOTE-1" {
			pickup++
		}
	}
	for _, c := range e.amr.Calls() {
		if c.Op == "RequestLeg" {
			moves++
		}
	}
	if pickup != 1 || moves != 0 {
		t.Fatalf("pickups %d, moves %d (the move was already picked up: its request is obsolete)", pickup, moves)
	}
}

func TestAStaleLegFailsTheTransportWithTimeoutAndIsCancelled(t *testing.T) {
	e := newEnv(t)
	v := e.mustAccept("rq-1", "TOTE-1", "ST1", pack, tr.PurposeOutbound)
	e.dispatch()
	e.clock.Advance(899 * time.Second)
	if n, _ := e.svc.Tick(ctx); n != 0 {
		t.Fatal("timed out early")
	}
	e.clock.Advance(time.Second)
	if n, err := e.svc.Tick(ctx); n != 1 || err != nil {
		t.Fatalf("Tick = %d %v", n, err)
	}
	if f, _ := e.last().(transportorder.TransportFailed); f.Reason != transportorder.FailTimeout || f.SubsystemReason != "LEG_TIMEOUT" {
		t.Fatalf("timeout = %+v", e.last())
	}
	e.dispatch()
	calls := e.amr.Calls()
	if last := calls[len(calls)-1]; last.Op != "CancelLeg" || last.Key != "cancel-"+v.OrderID+".1.1" {
		t.Fatalf("cancel = %+v", last)
	}
	if n, _ := e.svc.Tick(ctx); n != 0 {
		t.Fatal("an ended transport timed out twice")
	}
}
