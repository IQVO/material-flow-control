package http_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	ct "github.com/IQVO/material-flow-control/internal/domain/containertrack"
)

// carry (Task 18 review): kills mutant M1 — the fingerprint must hash the path, or a cancellation of another
// transport under the same key replays the first 202 and leaves task-1.2 running.
func TestTheSameKeyOnAnotherPathIsKeyReused(t *testing.T) {
	f := newFixture(t)
	f.stored("TOTE-1")
	f.stored("TOTE-2")
	if rec, body := f.do(route, "k-a", pick("task-1.1", "TOTE-1")); rec.Code != http.StatusAccepted {
		t.Fatalf("accept 1 = %d %v", rec.Code, body)
	}
	if rec, body := f.do(route, "k-b", pick("task-1.2", "TOTE-2")); rec.Code != http.StatusAccepted {
		t.Fatalf("accept 2 = %d %v", rec.Code, body)
	}
	if rec, body := f.do(route+"/task-1.1/cancellation", "k-c", nil); rec.Code != http.StatusAccepted {
		t.Fatalf("cancel 1 = %d %v", rec.Code, body)
	}
	rec, body := f.do(route+"/task-1.2/cancellation", "k-c", nil)
	if rec.Code != http.StatusConflict || body["code"] != "idempotency-key-reused" || replayed(rec) {
		t.Fatalf("same key, other path = %d replayed=%v %v, want 409 idempotency-key-reused", rec.Code, replayed(rec), body)
	}
	if get, view := f.do2(http.MethodGet, route+"/task-1.2"); get.Code != http.StatusOK || view["status"] != "IN_PROGRESS" {
		t.Fatalf("task-1.2 = %d %v, want IN_PROGRESS (not cancelled)", get.Code, view)
	}
}

// carry (Task 18 review): kills mutant M4 — a key over 128 characters is refused before the request runs.
func TestAKeyLongerThan128CharactersIsRefused(t *testing.T) {
	f := newFixture(t)
	rec, body := f.do(route, strings.Repeat("k", 129), pick("task-9.1", "TOTE-9"))
	if rec.Code != http.StatusBadRequest || !strings.HasPrefix(body["code"].(string), "idempotency-key-") {
		t.Fatalf("129-character key = %d %v, want 400 idempotency-key-*", rec.Code, body)
	}
	if n := len(f.events.All()); n != 0 {
		t.Fatalf("events = %v, want none: the request must not run", f.eventTypes())
	}
	if rec, body := f.do(route, strings.Repeat("k", 128), pick("task-9.1", "TOTE-9")); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("128-character key = %d %v, want the 422 rejection", rec.Code, body)
	}
}

// carry (Task 18 review): kills mutant M9 — finishRefused re-reads the key after its save, so a concurrent FINAL
// refusal with another body answers 409 idempotency-key-reused, not its own 400.
func TestAConcurrentFinalRefusalWithAnotherBodyAnswersKeyReused(t *testing.T) {
	f, _, st := loggedFixture(t)
	if rec, body := f.do(route, "k-f", map[string]any{"transport_request_id": "t-2"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("first = %d %v", rec.Code, body)
	}
	st.blind = 1
	rec, body := f.do(route, "k-f", map[string]any{"transport_request_id": "t-3"})
	if rec.Code != http.StatusConflict || body["code"] != "idempotency-key-reused" {
		t.Fatalf("second = %d %v, want 409 idempotency-key-reused", rec.Code, body)
	}
}

// carry (Task 19 review, INVESTIGATE / P3-R15b): a copy of a request that passed the key Lookup before
// the first copy committed (the blind Lookup stands for it), waited on the flow lock and then decided a
// RETRYABLE refusal (the first copy already lifted the hold: not-in-clarification) answers the stored
// 200 as a replay, not its own 409. The Postgres twin queues real copies on the held flow lock.
func TestAnInFlightDuplicateThatDecidesARetryableRefusalReplaysTheStoredAnswer(t *testing.T) {
	f, _, st := loggedFixture(t)
	tr := ct.New("TOTE-4", t0)
	tr.MoveTo(ct.Position{Kind: ct.InAsrs, Code: asrsLoc}, t0)
	tr.SendToClarification(ct.Clarification{Reason: ct.ReasonNoRead, DetectedAt: asrsLoc}, t0)
	if err := f.tracks.Save(context.Background(), tr); err != nil {
		t.Fatal(err)
	}
	path := "/container-tracks/TOTE-4/clarification-resolution"
	first, want := f.do(path, "k-dup", nil)
	if first.Code != http.StatusOK {
		t.Fatalf("first = %d %v", first.Code, want)
	}
	st.blind = 1
	dup, body := f.do(path, "k-dup", nil)
	if dup.Code != http.StatusOK || !replayed(dup) || body["container_id"] != want["container_id"] {
		t.Fatalf("in-flight duplicate = %d replayed=%v %v, want the first 200 replayed", dup.Code, replayed(dup), body)
	}
	st.blind = 1
	other, body := f.do(path, "k-dup", map[string]any{"location": asrsLoc})
	if other.Code != http.StatusConflict || body["code"] != "idempotency-key-reused" {
		t.Fatalf("in-flight copy with another body = %d %v, want 409 idempotency-key-reused", other.Code, body)
	}
}
