package a2a

import (
	"context"
	"fmt"
	"testing"

	"github.com/IQVO/material-flow-control/internal/application/usecases"
)

// carry (Task 21 review): every typed read error, wrapped the way the use cases wrap it, maps to its exact code,
// title, status and type URI (the REST adapter's vocabulary), and keeps its own message as the detail.
func TestEveryTypedReadErrorMapsToItsExactProblem(t *testing.T) {
	const base = "https://errors.material-flow-control.warehouse-systems.dev/"
	cases := []struct {
		err    error
		code   string
		title  string
		status int
	}{
		{usecases.ErrTrackNotFound, "container-track-not-found", "Container track not found", 404},
		{usecases.ErrInvalidRequest, "invalid-parameter", "Invalid parameter", 400},
		{usecases.ErrRequestNotFound, "transport-request-not-found", "Transport request not found", 404},
		{usecases.ErrStationNotFound, "station-not-found", "Station not found", 404},
		{usecases.ErrAsrsNotFound, "asrs-not-found", "AS/RS not found", 404},
	}
	if len(cases) != len(useCaseProblems) {
		t.Fatalf("useCaseProblems has %d entries, the table pins %d: pin the new one here", len(useCaseProblems), len(cases))
	}
	for _, c := range cases {
		wrapped := fmt.Errorf("read X-1: %w", c.err)
		p := problemFromUseCase(context.Background(), wrapped)
		if p.Code != c.code || p.Title != c.title || p.Status != c.status || p.Type != base+c.code || p.Detail != wrapped.Error() {
			t.Errorf("%v -> %+v, want code %s title %q status %d type %s%s", c.err, *p, c.code, c.title, c.status, base, c.code)
		}
	}
}
