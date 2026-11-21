package usecases_test

import (
	"errors"
	"testing"

	"github.com/IQVO/material-flow-control/internal/application/usecases"
	ct "github.com/IQVO/material-flow-control/internal/domain/containertrack"
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
