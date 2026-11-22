package http_test

import (
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	mfchttp "github.com/IQVO/material-flow-control/internal/adapters/inbound/http"
)

// ruling P4-R20: while the subsystem consumers have not caught up with the backlog sampled at boot, no POST is
// decided: each answers 503 dependency-unavailable (RETRYABLE, nothing recorded, no event), reads still answer, and
// the same key and body is decided normally once the gate opens.
func TestNoPostIsDecidedBeforeTheSubsystemFactsAreCaughtUp(t *testing.T) {
	var open atomic.Bool
	gate := func() error {
		if open.Load() {
			return nil
		}
		return errors.New("catching up with the subsystem facts published before boot: warehouse.asrs-control.events (3 facts behind)")
	}
	a := newAPI(t, func(s *mfchttp.Server) { s.CaughtUp = gate })
	a.stored("TOTE-1")
	for _, c := range []struct {
		path string
		body any
	}{{"/transport-requests", pick("task-1.1", "TOTE-1")}, {"/transport-requests/task-1.1/cancellation", nil},
		{"/container-tracks/TOTE-1/clarification-resolution", nil}} {
		rec, body := a.do(http.MethodPost, c.path, "k-1", c.body)
		if rec.Code != http.StatusServiceUnavailable || body["code"] != "dependency-unavailable" ||
			!strings.Contains(body["detail"].(string), "(3 facts behind)") {
			t.Fatalf("POST %s while catching up = %d %v, want 503 dependency-unavailable naming the lag", c.path, rec.Code, body)
		}
	}
	if n := len(a.events.All()); n != 0 {
		t.Fatalf("%d events while catching up, want none", n)
	}
	if rec, _ := a.do(http.MethodGet, "/subsystems", "", nil); rec.Code != http.StatusOK {
		t.Fatalf("GET /subsystems while catching up = %d, want 200 (reads are not gated)", rec.Code)
	}
	open.Store(true)
	rec, body := a.do(http.MethodPost, "/transport-requests", "k-1", pick("task-1.1", "TOTE-1"))
	if rec.Code != http.StatusAccepted || body["status"] != "IN_PROGRESS" || rec.Header().Get("Idempotency-Replayed") != "" {
		t.Fatalf("POST after the catch-up under the same key = %d %v %v, want a fresh 202", rec.Code, rec.Header(), body)
	}
}
