package http

import (
	"maps"
	"net/http"
	"testing"

	"github.com/IQVO/material-flow-control/internal/application/usecases"
	tr "github.com/IQVO/material-flow-control/internal/domain/transportrequest"
)

func TestEveryProblemDeclaresItsReplayClass(t *testing.T) {
	want := map[string]replayClass{"bad-request": classFinal, "invalid-parameter": classFinal, "transport-request-conflict": classFinal,
		"transport-request-not-found": classRetryable, "container-track-not-found": classRetryable, "station-not-found": classFinal, // ruling P4-R12
		"unknown-location": classFinal, "transport-ended": classRetryable, "transport-not-cancellable": classRetryable,
		"not-in-clarification": classRetryable, "transport-request-expired": classFinal} // ruling P4-R22
	// Two errors share invalid-parameter, so the missing set is a copy (the brief deleted from want itself).
	missing := maps.Clone(want)
	for _, e := range problemCatalogue {
		if c, ok := want[e.p.slug]; !ok || c != e.p.class {
			t.Fatalf("%s: class %s, want %s", e.p.slug, e.p.class, want[e.p.slug])
		}
		delete(missing, e.p.slug)
	}
	if len(missing) != 0 {
		t.Fatalf("missing problems: %v", missing)
	}
	for reason, slug := range map[tr.Reason]string{tr.ReasonUnknownContainer: "unknown-container", tr.ReasonContainerBusy: "container-busy",
		tr.ReasonDestinationUnreachable: "destination-unreachable", tr.ReasonSubsystemUnavailable: "subsystem-unavailable"} {
		p := rejectionProblem(reason)
		if p.slug != slug || p.status != http.StatusUnprocessableEntity || p.class != classOutcome {
			t.Fatalf("%s = %+v", reason, p)
		}
	}
	if problemFor(usecases.ErrTransportExpired).status != http.StatusGone {
		t.Fatal("transport-request-expired is a 410 (ruling P4-R22)")
	}
	if problemFor(usecases.ErrNotCancellable).status != http.StatusConflict {
		t.Fatal("transport-not-cancellable is a 409")
	}
}
