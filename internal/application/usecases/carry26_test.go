package usecases_test

import (
	"context"
	"testing"
	"time"

	"github.com/IQVO/material-flow-control/internal/adapters/outbound/memory"
	"github.com/IQVO/material-flow-control/internal/application/command"
	"github.com/IQVO/material-flow-control/internal/application/ports"
	"github.com/IQVO/material-flow-control/internal/application/subsystem"
	ct "github.com/IQVO/material-flow-control/internal/domain/containertrack"
	"github.com/IQVO/material-flow-control/internal/domain/transportorder"
	tr "github.com/IQVO/material-flow-control/internal/domain/transportrequest"
)

// carry (Task 11 review): the internal clarification MOVE of a NO_READ container is picked up, then times out. Tick
// fails it TIMEOUT with one best-effort CANCEL_LEG, and the hold keeps its FIRST reason (NO_READ, Since unchanged,
// ruling P4-R5) even though the timed-out leg had the container on the robot (ruling P4-R9): no second
// ContainerSentToClarification.
func TestATimedOutClarificationMoveKeepsTheFirstHold(t *testing.T) {
	e := newEnv(t)
	e.stored("TOTE-1")
	e.mustAccept("rq-1", "TOTE-1", asrsLoc, "ST1", tr.PurposePick)
	e.dispatch()
	o, err := e.svc.OrderDetail(ctx, "rq-1")
	e.must(err)
	e.presented(o.Legs[0].Ref, "TOTE-1", "OUT1", "NO_READ")
	heldAt := e.clock.Now()
	io := e.openInternal("TOTE-1")
	if io == nil {
		t.Fatal("no internal clarification order")
	}
	e.dispatch()
	e.pickedUp(io.ID()+".1.1", "TOTE-1", "ASRS1-OUT1")
	before := len(e.events.All())
	e.clock.Advance(900 * time.Second)
	if n, err := e.svc.Tick(ctx); n != 1 || err != nil {
		t.Fatalf("Tick = %d %v, want the clarification move timed out", n, err)
	}
	for _, ev := range e.events.All()[before:] {
		if _, ok := ev.(ct.ContainerSentToClarification); ok {
			t.Fatalf("a second ContainerSentToClarification after the timeout: %+v", e.events.All()[before:])
		}
	}
	track, err := e.tracks.Get(ctx, "TOTE-1")
	e.must(err)
	if c, held := track.Clarification(); !held || c.Reason != ct.ReasonNoRead || !c.Since.Equal(heldAt) {
		t.Fatalf("hold = %+v (held %v), want the first NO_READ since %s", c, held, heldAt)
	}
	got, err := e.orders.Get(ctx, io.ID())
	e.must(err)
	if s := got.Snapshot(); s.Status != transportorder.StatusFailed || s.Reason != transportorder.FailTimeout {
		t.Fatalf("internal order = %s %s, want FAILED TIMEOUT", s.Status, s.Reason)
	}
	if c := e.pending(command.CancelLeg); len(c) != 1 {
		t.Fatalf("CANCEL_LEG commands = %+v, want one best-effort cancel", c)
	}
}

// carry (Task 11 review): asrs-control accepted a RETRIEVE after its order ended while the CREATE_STORAGE_JOB call
// was in flight (here a late on-robot failure of the container's previous order fails the re-request rq-2, as in
// TestALegAcceptedAfterItsOrderEndedIsCancelled): the job id is recorded on the CANCELLED leg and a best-effort
// cancel under cancel-<ref> names that storage job.
func TestAStorageJobAcceptedAfterItsOrderEndedIsCancelled(t *testing.T) {
	var asrs *endingAsrs
	e := newEnvWithAsrs(t, func(a *memory.Asrs) ports.AsrsClient { asrs = &endingAsrs{Asrs: a}; return asrs })
	refA := e.timedOut()
	e.stored("TOTE-1")
	e.mustAccept("rq-2", "TOTE-1", asrsLoc, "ST1", tr.PurposePick)
	asrs.before = func() { e.legFailed(refA, "TOTE-1", "CANCELLED", true) }
	e.dispatch()
	o, err := e.svc.OrderDetail(ctx, "rq-2")
	e.must(err)
	leg := o.Legs[0]
	if o.Status != transportorder.StatusFailed || leg.Status != transportorder.LegCancelled || leg.SubsystemRef == "" {
		t.Fatalf("rq-2 = %s, leg %+v; want FAILED with the accepted job id on the CANCELLED leg", o.Status, leg)
	}
	e.dispatch()
	c := e.asrs.Calls()
	if last := c[len(c)-1]; last.Op != "CancelStorageJob" || last.Key != "cancel-"+leg.Ref || last.TargetID != leg.SubsystemRef {
		t.Fatalf("calls = %+v, want CancelStorageJob %s for %s", c, "cancel-"+leg.Ref, leg.SubsystemRef)
	}
}

// endingAsrs runs before once inside the next CreateStorageJob call (outside any unit of work, as the dispatcher
// makes it), then answers as the memory double.
type endingAsrs struct {
	*memory.Asrs
	before func()
}

func (a *endingAsrs) CreateStorageJob(ctx context.Context, key, asrsID string, r subsystem.StorageJobRequest) (subsystem.Accepted, error) {
	if f := a.before; f != nil {
		a.before = nil
		f()
	}
	return a.Asrs.CreateStorageJob(ctx, key, asrsID, r)
}
