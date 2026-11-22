package usecases_test

import (
	"errors"
	"testing"

	"github.com/IQVO/material-flow-control/internal/application/command"
	"github.com/IQVO/material-flow-control/internal/application/usecases"
	tr "github.com/IQVO/material-flow-control/internal/domain/transportrequest"
)

// ruling P4-R22: the retention purges an ended order but keeps its ACCEPTED request record. Every read and every
// retry of that transport_request_id then answers ErrTransportExpired (FINAL): never a 500, never a re-execution.
// The same id with another request is still a conflict, because the record (with the request) is kept.
func TestAnAcceptedRequestWhoseOrderWasPurgedIsExpiredAndNeverReExecuted(t *testing.T) {
	e := newEnv(t)
	e.stored("TOTE-1")
	first := e.mustAccept("rq-1", "TOTE-1", asrsLoc, "ST1", tr.PurposePick)
	e.store.PurgeOrder(first.OrderID)
	cmds, events := len(e.cmds.All()), len(e.events.All())
	if _, err := e.svc.Transport(ctx, "rq-1"); !errors.Is(err, usecases.ErrTransportExpired) {
		t.Fatalf("read of a purged transport = %v, want ErrTransportExpired", err)
	}
	if _, err := e.request("rq-1", "TOTE-1", asrsLoc, "ST1", tr.PurposePick); !errors.Is(err, usecases.ErrTransportExpired) {
		t.Fatalf("retry of a purged transport = %v, want ErrTransportExpired", err)
	}
	if _, err := e.request("rq-1", "TOTE-1", asrsLoc, "ST1", tr.PurposeReplenish); !errors.Is(err, usecases.ErrRequestConflict) {
		t.Fatalf("same id, another request = %v, want ErrRequestConflict (the record is kept)", err)
	}
	if _, err := e.svc.OrderDetail(ctx, "rq-1"); !errors.Is(err, usecases.ErrTransportExpired) {
		t.Fatalf("order detail of a purged transport = %v, want ErrTransportExpired", err)
	}
	if _, _, err := e.svc.CancelTransport(ctx, "rq-1"); !errors.Is(err, usecases.ErrTransportExpired) {
		t.Fatalf("cancel of a purged transport = %v, want ErrTransportExpired", err)
	}
	if len(e.cmds.All()) != cmds || len(e.events.All()) != events || len(e.pending(command.RequestLeg)) != 1 {
		t.Fatalf("an expired retry wrote: %d commands (was %d), %d events (was %d)", len(e.cmds.All()), cmds,
			len(e.events.All()), events)
	}
}
