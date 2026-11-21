package transportorder_test

import (
	"errors"
	"testing"
	"time"

	"github.com/IQVO/material-flow-control/internal/domain/routing"
	to "github.com/IQVO/material-flow-control/internal/domain/transportorder"
	tr "github.com/IQVO/material-flow-control/internal/domain/transportrequest"
)

// carry (Task 10 review): a failure fact names the kinds of leg it can end. LegFailedAs refuses a leg of another
// kind with ErrWrongLeg and leaves it unchanged; a matching kind ends it FAILED like LegFailed.
func TestLegFailedAsRefusesAFactForAnotherKindOfLegAndLeavesItUnchanged(t *testing.T) {
	o := accept(t, tr.PurposePick, asrs, station, pickPlan())
	o.Next(t0)
	before, _ := o.LegByRef("to-1.1.1")
	if err := o.LegFailedAs("to-1.1.1", t0.Add(time.Minute), routing.KindMove); !errors.Is(err, to.ErrWrongLeg) {
		t.Fatalf("a MOVE failure for a RETRIEVE = %v, want ErrWrongLeg", err)
	}
	if after, _ := o.LegByRef("to-1.1.1"); after != before || o.Ended() {
		t.Fatalf("the refused leg changed: %+v -> %+v (order ended %v)", before, after, o.Ended())
	}
	if err := o.LegFailedAs("nope", t0, routing.KindMove); !errors.Is(err, to.ErrUnknownLeg) {
		t.Fatalf("unknown reference = %v, want ErrUnknownLeg", err)
	}
	must(t, o.LegFailedAs("to-1.1.1", t0.Add(time.Minute), routing.KindRetrieve, routing.KindStore))
	if l, _ := o.LegByRef("to-1.1.1"); l.Status != to.LegFailed || !l.EndedAt.Equal(t0.Add(time.Minute)) {
		t.Fatalf("a RETRIEVE failure for a RETRIEVE: %+v", l)
	}
	if err := o.LegFailedAs("to-1.1.1", t0, routing.KindRetrieve); !errors.Is(err, to.ErrNotInFlight) {
		t.Fatalf("a second failure = %v, want ErrNotInFlight", err)
	}
}
