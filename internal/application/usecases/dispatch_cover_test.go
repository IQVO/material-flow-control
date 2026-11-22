package usecases_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/IQVO/material-flow-control/internal/adapters/outbound/memory"
	"github.com/IQVO/material-flow-control/internal/application/command"
	"github.com/IQVO/material-flow-control/internal/application/ports"
	"github.com/IQVO/material-flow-control/internal/application/subsystem"
	"github.com/IQVO/material-flow-control/internal/application/usecases"
	"github.com/IQVO/material-flow-control/internal/config"
	"github.com/IQVO/material-flow-control/internal/domain/transportorder"
	tr "github.com/IQVO/material-flow-control/internal/domain/transportrequest"
)

// ruling P4-R8 (Task 11 review): the dispatcher and Tick branches no brief test reaches.

func (e *env) unreachable(c interface{ Answer(...error) }, n int) {
	for i := 0; i < n; i++ {
		c.Answer(fmt.Errorf("dial: %w", subsystem.ErrUnreachable))
	}
}

func (e *env) only(kind command.Kind) command.Command {
	e.t.Helper()
	var got []command.Command
	for _, c := range e.cmds.All() {
		if c.Kind == kind {
			got = append(got, c)
		}
	}
	if len(got) != 1 {
		e.t.Fatalf("%s commands = %+v", kind, got)
	}
	return got[0]
}

func TestAnUnreachableCancelGivesUpAtTheDeadlineAndTheTransportRuns(t *testing.T) {
	e := newEnv(t)
	e.mustAccept("rq-1", "TOTE-1", "ST1", pack, tr.PurposeOutbound)
	e.dispatch()
	if _, _, err := e.svc.CancelTransport(ctx, "rq-1"); err != nil {
		t.Fatal(err)
	}
	e.unreachable(e.amr, 20)
	e.dispatch()
	if c := e.only(command.CancelLeg); c.Status != command.StatusPending || c.Attempts != 1 {
		t.Fatalf("cancel after one unreachable try = %+v", c)
	}
	e.clock.Advance(301 * time.Second)
	e.dispatch()
	if c := e.only(command.CancelLeg); c.Status != command.StatusFailed {
		t.Fatalf("cancel after the deadline = %+v", c)
	}
	if v, _ := e.svc.Transport(ctx, "rq-1"); v.Status != usecases.ViewInProgress {
		t.Fatalf("a cancel that expired lets the transport run: %+v", v)
	}
	if _, out, err := e.svc.CancelTransport(ctx, "rq-1"); err != nil || out != usecases.CancelAsked {
		t.Fatalf("re-asking after the expiry = %v %v", out, err)
	}
}

func (e *env) pickedUpAtOut1() {
	e.t.Helper()
	e.stored("TOTE-1")
	e.mustAccept("rq-1", "TOTE-1", asrsLoc, "ST1", tr.PurposePick)
	e.dispatch()
	o, _ := e.svc.OrderDetail(ctx, "rq-1")
	e.presented(o.Legs[0].Ref, "TOTE-1", "OUT1", "READ")
	e.pickedUp(e.lastRef(command.RequestLeg), "TOTE-1", "ASRS1-OUT1")
}

func TestAnUnreachablePickupConfirmationGivesUpAtTheDeadline(t *testing.T) {
	e := newEnv(t)
	e.pickedUpAtOut1()
	e.unreachable(e.asrs, 20)
	e.dispatch()
	if c := e.only(command.ConfirmPickup); c.Status != command.StatusPending || c.Attempts != 1 {
		t.Fatalf("pickup after one try = %+v", c)
	}
	e.clock.Advance(301 * time.Second)
	e.dispatch()
	if c := e.only(command.ConfirmPickup); c.Status != command.StatusFailed {
		t.Fatalf("pickup after the deadline = %+v", c)
	}
	if v, _ := e.svc.Transport(ctx, "rq-1"); v.Status != usecases.ViewInProgress {
		t.Fatalf("the transport is not ended by a pickup confirmation: %+v", v)
	}
}

func TestAFinalPickupRefusalFailsTheCommandOnly(t *testing.T) {
	e := newEnv(t)
	e.pickedUpAtOut1()
	e.asrs.Answer(&subsystem.Refusal{Status: 422, Code: "wrong-port-kind"})
	e.dispatch()
	if c := e.only(command.ConfirmPickup); c.Status != command.StatusFailed || c.Attempts != 1 {
		t.Fatalf("pickup = %+v", c)
	}
	if v, _ := e.svc.Transport(ctx, "rq-1"); v.Status != usecases.ViewInProgress {
		t.Fatalf("view = %+v", v)
	}
}

func TestACommandForAnUnknownOrderOrLegFailsWithoutACall(t *testing.T) {
	e := newEnv(t)
	v := e.mustAccept("rq-1", "TOTE-1", "ST1", pack, tr.PurposeOutbound)
	e.dispatch()
	e.must(e.cmds.Enqueue(ctx, command.New("cmd-x", "to-none", "to-none.1.1", command.RequestLeg, t0, time.Minute)))
	e.must(e.cmds.Enqueue(ctx, command.New("cmd-y", v.OrderID, "no-such-leg", command.CancelLeg, t0, time.Minute)))
	calls := len(e.amr.Calls())
	if n := e.dispatch(); n != 2 {
		t.Fatalf("dispatched %d", n)
	}
	want := map[string]string{"cmd-x": "order not found", "cmd-y": "leg not found"}
	for _, c := range e.cmds.All() {
		if w, ok := want[c.ID]; ok && (c.Status != command.StatusFailed || c.LastError != w) {
			t.Fatalf("%s = %+v, want FAILED %q", c.ID, c, w)
		}
	}
	if len(e.amr.Calls()) != calls {
		t.Fatalf("a subsystem was called: %+v", e.amr.Calls()[calls:])
	}
}

type failingCommands struct{ ports.Commands }

func (failingCommands) Due(context.Context, time.Time, int) ([]command.Command, error) {
	return nil, errRead
}

type failingOpen struct{ ports.TransportOrders }

func (failingOpen) Open(context.Context) ([]*transportorder.Order, error) { return nil, errRead }

func TestTheDispatcherNeedsBothClientsAndPassesStoreErrorsThrough(t *testing.T) {
	l, err := config.LoadLayout("../../../config/flow-layout.example.yaml")
	e := &env{t: t}
	e.must(err)
	d := coverageDeps(memory.NewStore())
	s, err := usecases.NewService(l, usecases.DefaultConfig(), d)
	e.must(err)
	if n, err := s.DispatchDue(ctx); n != 0 || err == nil || err.Error() != "usecases: the dispatcher needs both subsystem clients" {
		t.Fatalf("no clients = %d %v", n, err)
	}
	d.Asrs, d.Amr, d.Commands, d.Orders = memory.NewAsrs(), memory.NewAmr(), failingCommands{d.Commands}, failingOpen{d.Orders}
	s, err = usecases.NewService(l, usecases.DefaultConfig(), d)
	e.must(err)
	if _, err := s.DispatchDue(ctx); err != errRead {
		t.Fatalf("Due error = %v", err)
	}
	if _, err := s.Tick(ctx); err != errRead {
		t.Fatalf("Open error = %v", err)
	}
}

func TestOnlyALegTheSubsystemAcceptedCanGoStale(t *testing.T) {
	e := newEnv(t)
	e.mustAccept("rq-1", "TOTE-1", "ST1", pack, tr.PurposeOutbound) // PENDING: never requested
	e.clock.Advance(time.Hour)
	if n, err := e.svc.Tick(ctx); n != 0 || err != nil {
		t.Fatalf("a PENDING leg timed out: %d %v", n, err)
	}
	e.dispatch() // REQUESTED at t0+1h
	e.clock.Advance(899 * time.Second)
	if n, _ := e.svc.Tick(ctx); n != 0 {
		t.Fatal("timed out early")
	}
}
