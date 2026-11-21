package usecases_test

import (
	"context"
	"testing"

	"github.com/IQVO/material-flow-control/internal/adapters/outbound/memory"
	"github.com/IQVO/material-flow-control/internal/application/ports"
	"github.com/IQVO/material-flow-control/internal/application/subsystem"
	"github.com/IQVO/material-flow-control/internal/domain/transportorder"
	tr "github.com/IQVO/material-flow-control/internal/domain/transportrequest"
)

// endingAmr runs before once inside the next RequestLeg call (outside any unit of work, as the dispatcher makes
// it), then answers as the memory double.
type endingAmr struct {
	*memory.Amr
	before func()
}

func (a *endingAmr) RequestLeg(ctx context.Context, key, fleetID string, r subsystem.LegRequest) (subsystem.Accepted, error) {
	if f := a.before; f != nil {
		a.before = nil
		f()
	}
	return a.Amr.RequestLeg(ctx, key, fleetID, r)
}

// carry (Task 11 review): the order ended while its REQUEST_LEG was in flight (here a late on-robot failure of the
// container's previous order fails the re-request rq-2). amr-fleet-control accepted the leg anyway: its id is
// recorded on the CANCELLED leg and a best-effort CANCEL_LEG under cancel-<ref> stops the orphan.
func TestALegAcceptedAfterItsOrderEndedIsCancelled(t *testing.T) {
	var amr *endingAmr
	e := newEnvWith(t, func(a *memory.Amr) ports.AmrClient { amr = &endingAmr{Amr: a}; return amr })
	refA := e.timedOut()
	e.mustAccept("rq-2", "TOTE-1", "ST1", pack, tr.PurposeOutbound)
	amr.before = func() { e.legFailed(refA, "TOTE-1", "CANCELLED", true) }
	e.dispatch()
	o, err := e.svc.OrderDetail(ctx, "rq-2")
	e.must(err)
	leg := o.Legs[0]
	if o.Status != transportorder.StatusFailed || leg.Status != transportorder.LegCancelled || leg.SubsystemRef == "" {
		t.Fatalf("rq-2 = %s, leg %+v; want FAILED with the accepted id on the CANCELLED leg", o.Status, leg)
	}
	e.dispatch()
	c := e.amr.Calls()
	if last := c[len(c)-1]; last.Op != "CancelLeg" || last.Key != "cancel-"+leg.Ref || last.TargetID != leg.SubsystemRef {
		t.Fatalf("calls = %+v, want CancelLeg %s for %s", c, "cancel-"+leg.Ref, leg.SubsystemRef)
	}
}
