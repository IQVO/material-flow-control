package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	mfchttp "github.com/IQVO/material-flow-control/internal/adapters/inbound/http"
	"github.com/IQVO/material-flow-control/internal/adapters/outbound/memory"
	"github.com/IQVO/material-flow-control/internal/application/usecases"
	"github.com/IQVO/material-flow-control/internal/config"
	ct "github.com/IQVO/material-flow-control/internal/domain/containertrack"
)

var t0 = time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)

const asrsLoc = "WH1-AUTO-ASRS-ASRS1-00-00-S"

type api struct {
	t      *testing.T
	h      http.Handler
	events *memory.Events
	tracks *memory.Tracks
	orders *memory.Orders
}

func newAPI(t *testing.T) *api {
	t.Helper()
	l, err := config.LoadLayout("../../../../config/flow-layout.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	s := memory.NewStore()
	clock := memory.NewClock(t0)
	a := &api{t: t, events: memory.NewEvents(s), tracks: memory.NewTracks(s), orders: memory.NewOrders(s)}
	uow := memory.NewUnitOfWork(s)
	svc, err := usecases.NewService(l, usecases.DefaultConfig(), usecases.Deps{UoW: uow, Lock: memory.FlowLock{},
		Requests: memory.NewRequests(s), Orders: a.orders, Tracks: a.tracks, Stations: memory.NewStations(s),
		Capacities: memory.NewCapacities(s), Units: memory.NewUnits(s), Commands: memory.NewCommands(s), Inbox: memory.NewInbox(s),
		Sink: a.events, Asrs: memory.NewAsrs(), Amr: memory.NewAmr(), Clock: clock, IDs: memory.NewIDs("to"),
		Metrics: &memory.Metrics{}, Kick: &memory.Kicker{}, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	idem := mfchttp.NewIdempotency(memory.NewIdempotency(s), uow, clock)
	a.h = mfchttp.NewRouter(&mfchttp.Server{Service: svc, Idempotency: idem.Middleware, Readiness: mfchttp.NewReadiness(time.Second, time.Second)},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	return a
}

func (a *api) stored(container string) {
	a.t.Helper()
	tr := ct.New(container, t0)
	tr.MoveTo(ct.Position{Kind: ct.InAsrs, Code: asrsLoc}, t0)
	if err := a.tracks.Save(context.Background(), tr); err != nil {
		a.t.Fatal(err)
	}
}

func (a *api) do(method, path, key string, body any) (*httptest.ResponseRecorder, map[string]any) {
	a.t.Helper()
	var rd io.Reader = http.NoBody
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rd)
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	rec := httptest.NewRecorder()
	a.h.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec, out
}

func pick(id, container string) map[string]any {
	return map[string]any{"transport_request_id": id, "container_id": container, "source": asrsLoc, "destination": "ST1",
		"purpose": "PICK", "priority": "URGENT", "due_by": "2026-11-20T09:30:00Z"}
}

func TestAnAcceptedRequestAnswers202WithItsLocationAndReplaysUnderItsKey(t *testing.T) {
	a := newAPI(t)
	a.stored("TOTE-1")
	rec, body := a.do(http.MethodPost, "/transport-requests", "k-1", pick("task-1.1", "TOTE-1"))
	if rec.Code != http.StatusAccepted || rec.Header().Get("Location") != "/transport-requests/task-1.1" ||
		body["status"] != "IN_PROGRESS" || body["legs_total"] != float64(2) || body["legs_done"] != float64(0) ||
		body["transport_order_id"] == "" || body["accepted_at"] != "2026-11-20T08:00:00Z" || body["container_location"] != asrsLoc {
		t.Fatalf("accept = %d %v %v", rec.Code, rec.Header(), body)
	}
	again, body2 := a.do(http.MethodPost, "/transport-requests", "k-1", pick("task-1.1", "TOTE-1"))
	if again.Code != http.StatusAccepted || again.Header().Get("Idempotency-Replayed") != "true" || body2["transport_order_id"] != body["transport_order_id"] {
		t.Fatalf("replay = %d %v %v", again.Code, again.Header(), body2)
	}
	assertTheSameIDUnderANewKeyAnswersTheOriginalOutcome(t, a, body)
}

// Split from the brief's test (plan 04 Task 18) to stay under cyclop 15; the assertions are the brief's, unchanged.
func assertTheSameIDUnderANewKeyAnswersTheOriginalOutcome(t *testing.T, a *api, body map[string]any) {
	t.Helper()
	other, body3 := a.do(http.MethodPost, "/transport-requests", "k-2", pick("task-1.1", "TOTE-1"))
	if other.Code != http.StatusAccepted || other.Header().Get("Idempotency-Replayed") != "" || body3["transport_order_id"] != body["transport_order_id"] {
		t.Fatalf("same transport_request_id under a new key = %d %v (decision D7: the original outcome)", other.Code, body3)
	}
	open, _ := a.orders.Open(context.Background())
	if len(open) != 1 {
		t.Fatalf("orders = %d, want exactly one", len(open))
	}
}

func TestARejectionIs422WithItsReasonCommittedOnceAndReplayed(t *testing.T) {
	a := newAPI(t)
	rec, body := a.do(http.MethodPost, "/transport-requests", "k-1", pick("task-9.1", "TOTE-9"))
	if rec.Code != http.StatusUnprocessableEntity || body["code"] != "unknown-container" || body["reason"] != "UNKNOWN_CONTAINER" ||
		body["transport_request_id"] != "task-9.1" || rec.Header().Get("Content-Type") != "application/problem+json" {
		t.Fatalf("reject = %d %v", rec.Code, body)
	}
	replay, body2 := a.do(http.MethodPost, "/transport-requests", "k-1", pick("task-9.1", "TOTE-9"))
	if replay.Code != http.StatusUnprocessableEntity || replay.Header().Get("Idempotency-Replayed") != "true" || body2["reason"] != "UNKNOWN_CONTAINER" {
		t.Fatalf("replay = %d %v", replay.Code, body2)
	}
	if n := len(a.events.All()); n != 1 {
		t.Fatalf("TransportRejected committed %d times, want 1 (OUTCOME: the record and the event commit with the answer)", n)
	}
	get, view := a.do(http.MethodGet, "/transport-requests/task-9.1", "", nil)
	if get.Code != http.StatusOK || view["status"] != "REJECTED" || view["reason"] != "UNKNOWN_CONTAINER" || view["transport_order_id"] != nil {
		t.Fatalf("rejected view = %d %v", get.Code, view)
	}
}

func TestMalformedConflictingAndKeylessRequestsAreFinalProblems(t *testing.T) {
	a := newAPI(t)
	a.stored("TOTE-1")
	cases := []struct {
		key  string
		body any
		code int
		slug string
	}{
		{"", pick("t-1", "TOTE-1"), http.StatusBadRequest, "idempotency-key-required"},
		{"k-a", map[string]any{"transport_request_id": "t-2"}, http.StatusBadRequest, "invalid-parameter"},
		{"k-b", map[string]any{"transport_request_id": "t-3", "container_id": "TOTE-1", "source": asrsLoc, "destination": "ST1",
			"purpose": "PICK", "priority": "URGENT", "due_by": "tomorrow"}, http.StatusBadRequest, "invalid-parameter"},
		{"k-c", map[string]any{"transport_request_id": "t-4", "container_id": "TOTE-1", "source": asrsLoc, "destination": asrsLoc,
			"purpose": "PUTAWAY", "priority": "NORMAL", "due_by": "2026-11-20T09:30:00Z"}, http.StatusBadRequest, "invalid-parameter"},
		{"k-d", "not an object", http.StatusBadRequest, "bad-request"},
	}
	for _, c := range cases {
		rec, body := a.do(http.MethodPost, "/transport-requests", c.key, c.body)
		if rec.Code != c.code || body["code"] != c.slug {
			t.Fatalf("%v: %d %v, want %d %s", c.body, rec.Code, body, c.code, c.slug)
		}
	}
	a.do(http.MethodPost, "/transport-requests", "k-e", pick("t-5", "TOTE-1"))
	changed := pick("t-5", "TOTE-1")
	changed["destination"] = "WH1-PCK-PK1"
	changed["purpose"] = "REPLENISH"
	if rec, body := a.do(http.MethodPost, "/transport-requests", "k-f", changed); rec.Code != http.StatusConflict || body["code"] != "transport-request-conflict" {
		t.Fatalf("conflict = %d %v", rec.Code, body)
	}
}

func TestCancellationAnswers202InFlightAnd409OnceEnded(t *testing.T) {
	a := newAPI(t)
	a.stored("TOTE-1")
	a.do(http.MethodPost, "/transport-requests", "k-1", pick("task-1.1", "TOTE-1"))
	rec, body := a.do(http.MethodPost, "/transport-requests/task-1.1/cancellation", "k-2", nil)
	if rec.Code != http.StatusAccepted || body["status"] != "CANCEL_REQUESTED" {
		t.Fatalf("cancel in flight = %d %v", rec.Code, body)
	}
	if rec, body := a.do(http.MethodPost, "/transport-requests/nope/cancellation", "k-3", nil); rec.Code != http.StatusNotFound ||
		body["code"] != "transport-request-not-found" {
		t.Fatalf("unknown = %d %v", rec.Code, body)
	}
	a.do(http.MethodPost, "/transport-requests", "k-4", pick("task-9.1", "TOTE-9"))
	if rec, body := a.do(http.MethodPost, "/transport-requests/task-9.1/cancellation", "k-5", nil); rec.Code != http.StatusConflict ||
		body["code"] != "transport-ended" {
		t.Fatalf("rejected = %d %v", rec.Code, body)
	}
	if again, _ := a.do(http.MethodPost, "/transport-requests/task-9.1/cancellation", "k-5", nil); again.Header().Get("Idempotency-Replayed") == "true" {
		t.Fatal("a RETRYABLE refusal was stored")
	}
}

func TestTracksStationsAndSubsystemsAreReadable(t *testing.T) {
	a := newAPI(t)
	a.stored("TOTE-1")
	rec, body := a.do(http.MethodGet, "/container-tracks/TOTE-1", "", nil)
	pos, _ := body["position"].(map[string]any)
	if rec.Code != http.StatusOK || pos["kind"] != "IN_ASRS" || pos["code"] != asrsLoc || pos["robot"] != nil {
		t.Fatalf("track = %d %v", rec.Code, body)
	}
	if rec, body := a.do(http.MethodGet, "/container-tracks/NOPE", "", nil); rec.Code != http.StatusNotFound || body["code"] != "container-track-not-found" {
		t.Fatalf("no track = %d %v", rec.Code, body)
	}
	if rec, body := a.do(http.MethodPost, "/container-tracks/TOTE-1/clarification-resolution", "k-1", nil); rec.Code != http.StatusConflict ||
		body["code"] != "not-in-clarification" {
		t.Fatalf("not held = %d %v", rec.Code, body)
	}
	assertStationsAndSubsystemsAreReadable(t, a)
}

// Split from the brief's test (plan 04 Task 18) to stay under cyclop 15; the assertions are the brief's, unchanged.
func assertStationsAndSubsystemsAreReadable(t *testing.T, a *api) {
	t.Helper()
	rec, body := a.do(http.MethodGet, "/stations/ST1", "", nil)
	if rec.Code != http.StatusOK || body["station_id"] != "ST1" || body["buffer_limit"] != float64(2) || body["occupied"] != float64(0) {
		t.Fatalf("station = %d %v", rec.Code, body)
	}
	if rec, body := a.do(http.MethodGet, "/stations/ST9", "", nil); rec.Code != http.StatusNotFound || body["code"] != "station-not-found" {
		t.Fatalf("no station = %d %v", rec.Code, body)
	}
	rec, body = a.do(http.MethodGet, "/subsystems", "", nil)
	if subs, ok := body["subsystems"].([]any); rec.Code != http.StatusOK || !ok {
		t.Fatalf("subsystems = %d %v %v", rec.Code, body, subs)
	}
}
