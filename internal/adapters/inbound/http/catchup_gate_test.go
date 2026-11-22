package http_test

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	mfchttp "github.com/IQVO/material-flow-control/internal/adapters/inbound/http"
)

// catchUpDetail is the POST 503 detail while catching up (carry (Task 26 review): no raw gate error in it).
const catchUpDetail = "not ready to decide: the subsystem facts published before boot are still being applied; retry later"

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
	var logged bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	a := newAPI(t, func(s *mfchttp.Server) { s.CaughtUp = gate })
	a.stored("TOTE-1")
	for _, c := range []struct {
		path string
		body any
	}{{"/transport-requests", pick("task-1.1", "TOTE-1")}, {"/transport-requests/task-1.1/cancellation", nil},
		{"/container-tracks/TOTE-1/clarification-resolution", nil}} {
		rec, body := a.do(http.MethodPost, c.path, "k-1", c.body)
		// carry (Task 26 review): the detail is generic (no topic, lag or broker error reaches the caller); the gate's
		// own error goes to the log.
		if rec.Code != http.StatusServiceUnavailable || body["code"] != "dependency-unavailable" || body["detail"] != catchUpDetail {
			t.Fatalf("POST %s while catching up = %d %v, want 503 dependency-unavailable %q", c.path, rec.Code, body, catchUpDetail)
		}
	}
	if !strings.Contains(logged.String(), "(3 facts behind)") {
		t.Fatalf("the gate's error is not logged; log:\n%s", logged.String())
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
