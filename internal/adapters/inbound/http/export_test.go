package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/IQVO/material-flow-control/internal/domain/transportrequest"
)

// ProblemEntry exposes the production catalogue to the replay-class test.
type ProblemEntry struct {
	Slug   string
	Status int
	Class  string
}

// allProblems lists every problem type once (two errors share invalid-parameter), the four OUTCOME
// rejections included (writeRejection answers them outside problemCatalogue).
func allProblems() []problem {
	out := []problem{idempotencyKeyRequired, idempotencyKeyReused, internalProblem, dependencyUnavailable}
	for _, r := range []transportrequest.Reason{transportrequest.ReasonUnknownContainer, transportrequest.ReasonContainerBusy,
		transportrequest.ReasonDestinationUnreachable, transportrequest.ReasonSubsystemUnavailable} {
		out = append(out, rejectionProblem(r))
	}
	seen := map[string]bool{}
	for _, p := range out {
		seen[p.slug] = true
	}
	for _, e := range problemCatalogue {
		if !seen[e.p.slug] {
			seen[e.p.slug] = true
			out = append(out, e.p)
		}
	}
	return out
}

func Catalogue() []ProblemEntry {
	var out []ProblemEntry
	for _, p := range allProblems() {
		out = append(out, ProblemEntry{Slug: p.slug, Status: p.status, Class: string(p.class)})
	}
	return out
}

// Refuse answers slug through the production writer, so the class marking runs.
func Refuse(w http.ResponseWriter, r *http.Request, slug string) {
	for _, p := range allProblems() {
		if p.slug == slug {
			writeProblem(w, r, p, "refused")
			return
		}
	}
	panic("unknown problem slug " + slug)
}

// PanicPath is the POST route WithPanicRoute adds: its handler panics inside the Idempotency middleware.
const PanicPath = "/transport-requests/panic-probe"

// WithPanicRoute adds PanicPath to a NewRouter router, behind idem, so a test drives a 5xx through the
// production middleware chain (recoverPanic answers the 500; the unit of work rolls back).
func WithPanicRoute(h http.Handler, idem func(http.Handler) http.Handler) http.Handler {
	mux := h.(*chi.Mux)
	mux.With(idem).Post(PanicPath, func(http.ResponseWriter, *http.Request) { panic("handler panic") })
	return mux
}
