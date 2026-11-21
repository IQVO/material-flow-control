package transportorder_test

import (
	"errors"
	"testing"
	"time"

	to "github.com/IQVO/material-flow-control/internal/domain/transportorder"
	tr "github.com/IQVO/material-flow-control/internal/domain/transportrequest"
)

// ruling P4-R6 (D15 read by intent): while the container sits on an AS/RS OUTPUT port no robot has picked it up
// yet, so the transport is not cancellable whatever the current leg's status (a cancel would end it with the tote
// on OUT1, no CONFIRM_PICKUP and the port blocked). Nothing is recorded: CancelRequested stays false.
func TestAMoveFromAnOutputPortIsNotCancellableBeforeThePickup(t *testing.T) {
	o := accept(t, tr.PurposePick, asrs, station, pickPlan())
	o.Next(t0)
	must(t, o.Presented("to-1.1.1", out1, t0))
	o.Next(t0)
	if _, err := o.Admitted(t0); err != nil {
		t.Fatal(err)
	}
	if d, _, err := o.RequestCancel(t0); !errors.Is(err, to.ErrNotCancellable) || o.CancelRequested() {
		t.Fatalf("PENDING move from OUT1 = %s %v, cancel requested %v", d, err, o.CancelRequested())
	}
	must(t, o.Requested("to-1.2.1", "leg-a", t0))
	must(t, o.Assigned("to-1.2.1", "EMU/R1", t0))
	if d, _, err := o.RequestCancel(t0); !errors.Is(err, to.ErrNotCancellable) || o.CancelRequested() {
		t.Fatalf("ASSIGNED move from OUT1 = %s %v", d, err)
	}
}

// ruling P4-R6: a retried MOVE from the output port is still not cancellable.
func TestARetriedMoveFromAnOutputPortIsNotCancellable(t *testing.T) {
	o := accept(t, tr.PurposePick, asrs, station, pickPlan())
	o.Next(t0)
	must(t, o.Presented("to-1.1.1", out1, t0))
	o.Next(t0)
	if _, err := o.Admitted(t0); err != nil {
		t.Fatal(err)
	}
	must(t, o.LegFailed("to-1.2.1", t0.Add(time.Minute)))
	retry, err := o.RetryLeg("to-1.2.1", t0.Add(time.Minute))
	if err != nil || retry.From != out1 {
		t.Fatalf("retry = %+v %v", retry, err)
	}
	if d, _, err := o.RequestCancel(t0); !errors.Is(err, to.ErrNotCancellable) || o.CancelRequested() {
		t.Fatalf("retried move from OUT1 = %s %v", d, err)
	}
}
