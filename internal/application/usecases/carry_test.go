package usecases_test

import (
	"errors"
	"testing"
	"time"

	"github.com/IQVO/material-flow-control/internal/adapters/outbound/memory"
	"github.com/IQVO/material-flow-control/internal/application/command"
	"github.com/IQVO/material-flow-control/internal/application/usecases"
	ct "github.com/IQVO/material-flow-control/internal/domain/containertrack"
	"github.com/IQVO/material-flow-control/internal/domain/routing"
	"github.com/IQVO/material-flow-control/internal/domain/transportorder"
	tr "github.com/IQVO/material-flow-control/internal/domain/transportrequest"
)

// carry (Task 3 review): routing.Plan trusts its callers to pass layout members, so the use case must reject an
// unresolvable source and an unresolvable destination itself, DESTINATION_UNREACHABLE, before routing runs. The
// detail is the use case's own (routing's would read "destination unreachable"), and no track is touched.
func TestAnUnresolvableSourceOrDestinationIsUnreachableBeforeRouting(t *testing.T) {
	e := newEnv(t)
	e.stored("TOTE-1")
	const asrs2 = "WH1-AUTO-ASRS-ASRS2-00-00-S"
	cases := []struct {
		id, src, dst string
		p            tr.Purpose
	}{
		{"rq-src", asrs2, "ST1", tr.PurposePick},
		{"rq-dst", decant, asrs2, tr.PurposePutaway},
	}
	for _, c := range cases {
		_, err := e.request(c.id, "TOTE-1", c.src, c.dst, c.p)
		var r *usecases.RejectedError
		if !errors.As(err, &r) || r.Reason != tr.ReasonDestinationUnreachable ||
			r.Detail != "the source or the destination is not a place of this automation" {
			t.Fatalf("%s %s->%s = %v, want DESTINATION_UNREACHABLE before routing", c.id, c.src, c.dst, err)
		}
	}
	if tk, _ := e.tracks.Get(ctx, "TOTE-1"); tk.Position() != (ct.Position{Kind: ct.InAsrs, Code: asrsLoc}) || tk.ActiveOrderID() != "" {
		t.Fatalf("a rejection touched the track: %+v", tk.Snapshot())
	}
	want := []string{"transportrequest.TransportRejected", "transportrequest.TransportRejected"}
	if got := e.types(); !equal(got, want) {
		t.Fatalf("events = %v", got)
	}
}

// ruling P4-R6, use-case side: a transport whose container waits on the output port for its (admitted, PENDING)
// MOVE answers ErrNotCancellable (REST 409 transport-not-cancellable) and queues no CANCEL_LEG.
func TestCancellingAMoveFromAnOutputPortIsNotCancellable(t *testing.T) {
	e := newEnv(t)
	l := e.svc.Layout()
	src, _ := l.Resolve(asrsLoc)
	dst, _ := l.Resolve("ST1")
	out1, _ := l.Resolve(out1Loc)
	plan, err := routing.Plan(l, src, dst)
	if err != nil {
		t.Fatal(err)
	}
	req := tr.Request{ID: "rq-p", ContainerID: "TOTE-1", Source: asrsLoc, Destination: "ST1", Purpose: tr.PurposePick,
		Priority: tr.PriorityNormal, DueBy: t0.Add(time.Hour)}
	o, err := transportorder.Accept("to-p", req, src, dst, plan, t0)
	if err != nil {
		t.Fatal(err)
	}
	o.Next(t0)
	if err := o.Presented("to-p.1.1", out1, t0); err != nil {
		t.Fatal(err)
	}
	o.Next(t0)
	if _, err := o.Admitted(t0); err != nil {
		t.Fatal(err)
	}
	// carry (Task 9 review): a failed seed insert fails the test instead of passing on an empty store.
	if err := e.orders.Insert(ctx, o); err != nil {
		t.Fatal(err)
	}
	if err := memory.NewRequests(e.store).Insert(ctx, tr.Accept(req, "to-p", t0)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.svc.CancelTransport(ctx, "rq-p"); !errors.Is(err, usecases.ErrNotCancellable) {
		t.Fatalf("cancel with the container on OUT1 = %v, want ErrNotCancellable", err)
	}
	if c := e.pending(command.CancelLeg); len(c) != 0 {
		t.Fatalf("cancel commands = %+v", c)
	}
	if got, _ := e.orders.Get(ctx, "to-p"); got.CancelRequested() {
		t.Fatal("a refused cancellation was recorded")
	}
}
