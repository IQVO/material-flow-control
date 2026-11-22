// templates/idempotency/replay_classes_test.go.tmpl — the fleet reference test for
// idempotency replay classes (FINAL, OUTCOME, RETRYABLE).
//
// Copy to internal/adapters/inbound/http/idempotency_replay_classes_test.go (the package that owns
// the Idempotency-Key middleware), substitute the three placeholders and implement repoAdapter
// at the bottom (the only repo-specific code, marked SEARCH-FOR). Everything above it is verbatim.
//
//	http           the package name of the middleware's directory (the test is http_test)
//	/transport-requests         one idempotent route of the service, e.g. /orders (a POST the middleware wraps)
//	idempotency-key-reused   the slug of the 409 the middleware answers for a key reused with another
//	                  request, e.g. idempotency-key-reused
//
// The tests drive the REAL middleware, built by the repo's own constructor, with a FAKE handler
// that answers what a handler of each class answers. They know nothing of the store: the repo's
// design (key row in the request transaction, a savepoint, a separate write) hides behind
// replayAdapter. The tests must pass on every design; what differs is only how the adapter counts.
//
// Fleet rule: .claude/rules/fleet/idempotency-and-outbox.md. Standard and adoption steps:
// docs/idempotency-replay-classes.md (warehouse-harness-template). A real store (testcontainers)
// is the strongest adapter for key-in-transaction designs; an in-memory adapter is enough for a
// design whose store is behind a port.
package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	ahttp "github.com/IQVO/material-flow-control/internal/adapters/inbound/http"
	"github.com/IQVO/material-flow-control/internal/adapters/outbound/memory"
	"github.com/IQVO/material-flow-control/internal/application/ports"
	"github.com/IQVO/material-flow-control/internal/application/usecases"
	"github.com/IQVO/material-flow-control/internal/config"
)

// Replay classes, as the catalogue entry declares them. The empty string is "unclassified".
const (
	classFinal     = "FINAL"
	classOutcome   = "OUTCOME"
	classRetryable = "RETRYABLE"
)

const (
	idempotencyKeyHeader      = "Idempotency-Key"
	idempotencyReplayedHeader = "Idempotency-Replayed"
	route                     = "/transport-requests"
	keyReusedSlug             = "idempotency-key-reused"
)

// problemEntry is one problem type of the service's catalogue, as the test needs it.
type problemEntry struct {
	Slug   string
	Status int
	Class  string // the class the catalogue entry declares; "" when it declares none
}

// replayAdapter is the ONLY thing a repo implements. One adapter value = one clean store.
//
//   - Catalogue: every problem type the service can answer on an idempotent route, read from the
//     production catalogue (NOT a copy of it), each with the class its entry declares.
//   - Middleware: the production Idempotency-Key middleware, from the repo's own constructor, over
//     this adapter's store, wrapping next.
//   - Refuse: answer e exactly as production does — through the production error writer, so the
//     class the catalogue declares is what marks the capturing writer (DoNotCache, KeepWrites, ...).
//   - Write: one observable write made THROUGH the request's context (r.Context()), i.e. in the
//     same unit of work / transaction the middleware opened for this request. It stands for the
//     aggregate and the outbox rows a real command commits.
//   - Committed: how many writes tagged tag are visible OUTSIDE any request (committed).
//   - KeyRows: how many rows the store holds for key (0 after the key was released).
type replayAdapter interface {
	Catalogue() []problemEntry
	Middleware(t *testing.T, next http.Handler) http.Handler
	Refuse(w http.ResponseWriter, r *http.Request, e problemEntry)
	Write(ctx context.Context, tag string) error
	Committed(t *testing.T, tag string) int
	KeyRows(t *testing.T, key string) int
}

// answerFn is what the fake handler does for one call.
type answerFn func(w http.ResponseWriter, r *http.Request)

// fakeHandler stands for the use case behind the middleware. It counts how often it RAN and
// answers from script in order (the last answer repeats). A write is made by the answer itself.
type fakeHandler struct {
	script []answerFn
	calls  int
}

func (h *fakeHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	i := h.calls
	h.calls++
	if i >= len(h.script) {
		i = len(h.script) - 1
	}
	h.script[i](w, r)
}

// refusing answers entry e; for an OUTCOME entry the command commits its write first.
func refusing(a replayAdapter, e problemEntry, tag string) answerFn {
	return func(w http.ResponseWriter, r *http.Request) {
		if e.Class == classOutcome {
			if err := a.Write(r.Context(), tag); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		a.Refuse(w, r, e)
	}
}

// succeeding commits one write and answers 201.
func succeeding(a replayAdapter, tag string) answerFn {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := a.Write(r.Context(), tag); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"id":"created"}`)
	}
}

func post(t *testing.T, h http.Handler, key, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, route, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(idempotencyKeyHeader, key)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func firstOf(t *testing.T, a replayAdapter, class string) problemEntry {
	t.Helper()
	for _, e := range a.Catalogue() {
		if e.Class == class && e.Status < http.StatusInternalServerError {
			return e
		}
	}
	return problemEntry{}
}

func replayed(rec *httptest.ResponseRecorder) bool {
	return rec.Header().Get(idempotencyReplayedHeader) == "true"
}

// classDeclared reports whether c is one of the three classes.
func classDeclared(c string) bool {
	return c == classFinal || c == classOutcome || c == classRetryable
}

// (a) Every catalogue entry declares exactly one class; an unclassified entry fails. There is no
// runtime default: a new problem type is classified by the PR that adds it, or this test is red.
func TestEveryProblemTypeDeclaresAReplayClass(t *testing.T) {
	entries := newReplayAdapter(t).Catalogue()
	if len(entries) == 0 {
		t.Fatal("the catalogue accessor returned no problem types: the test would pass on nothing")
	}
	for _, e := range entries {
		t.Run(e.Slug, func(t *testing.T) {
			if !classDeclared(e.Class) {
				t.Errorf("problem type %q (%d) declares no replay class (%q): set FINAL, OUTCOME or RETRYABLE on its catalogue entry (.claude/rules/fleet/idempotency-and-outbox.md)", e.Slug, e.Status, e.Class)
			}
			if e.Status >= http.StatusInternalServerError && e.Class != classRetryable {
				t.Errorf("problem type %q answers %d: a 5xx is never stored, so its class is RETRYABLE, got %q", e.Slug, e.Status, e.Class)
			}
		})
	}
}

// The check above must bite: an unclassified entry and a misspelt class are refused.
func TestTheUnclassifiedEntryCheckBites(t *testing.T) {
	for _, c := range []string{"", "final", "STORED", "OUTCOME "} {
		if classDeclared(c) {
			t.Errorf("class %q is accepted; only FINAL, OUTCOME and RETRYABLE are classes", c)
		}
	}
	for _, c := range []string{classFinal, classOutcome, classRetryable} {
		if !classDeclared(c) {
			t.Errorf("class %q is refused", c)
		}
	}
}

// (b) For EVERY catalogue entry: the first request is answered with that problem, then the same
// request again under the same key. By class: FINAL and OUTCOME are stored and replayed (the
// handler ran once); RETRYABLE (and every 5xx) is not stored and runs again.
// (The body of the template's test is split into checkFirstAnswer/checkSecondAnswer to stay
// under this repo's complexity limits; the assertions are the fleet template's, unchanged.)
func TestEveryProblemTypeIsReplayedOrReExecutedByItsClass(t *testing.T) {
	for _, e := range newReplayAdapter(t).Catalogue() {
		t.Run(e.Slug+"/"+e.Class, func(t *testing.T) {
			if !classDeclared(e.Class) {
				t.Skip("unclassified: reported by TestEveryProblemTypeDeclaresAReplayClass")
			}
			a := newReplayAdapter(t)
			h := &fakeHandler{script: []answerFn{refusing(a, e, e.Slug)}}
			mw := a.Middleware(t, h)
			key := "key-" + e.Slug
			stored := e.Class != classRetryable && e.Status < http.StatusInternalServerError
			wantWrites := 0
			if e.Class == classOutcome {
				wantWrites = 1
			}

			first := checkFirstAnswer(t, a, mw, e, key, stored, wantWrites)
			second := post(t, mw, key, `{"n":1}`)
			if second.Code != e.Status {
				t.Errorf("second answer status = %d, want %d", second.Code, e.Status)
			}
			checkSecondAnswer(t, h, e, first, second, stored)
			if got := a.Committed(t, e.Slug); got != wantWrites {
				t.Errorf("committed writes after the second request = %d, want %d: a replay records nothing new", got, wantWrites)
			}
		})
	}
}

func checkFirstAnswer(t *testing.T, a replayAdapter, mw http.Handler, e problemEntry, key string, stored bool, wantWrites int) *httptest.ResponseRecorder {
	t.Helper()
	first := post(t, mw, key, `{"n":1}`)
	if first.Code != e.Status {
		t.Fatalf("first answer status = %d, want %d (%s)", first.Code, e.Status, first.Body.String())
	}
	if replayed(first) {
		t.Errorf("the first answer carries %s: true", idempotencyReplayedHeader)
	}
	wantRows := 0
	if stored {
		wantRows = 1
	}
	if got := a.KeyRows(t, key); got != wantRows {
		t.Errorf("key rows after the first answer = %d, want %d", got, wantRows)
	}
	if got := a.Committed(t, e.Slug); got != wantWrites {
		t.Errorf("committed writes after the first answer = %d, want %d", got, wantWrites)
	}
	return first
}

func checkSecondAnswer(t *testing.T, h *fakeHandler, e problemEntry, first, second *httptest.ResponseRecorder, stored bool) {
	t.Helper()
	if !stored {
		if h.calls != 2 {
			t.Errorf("handler ran %d times, want 2: a %s answer is not stored, the retry executes", h.calls, e.Class)
		}
		if replayed(second) {
			t.Errorf("a %s answer was replayed", e.Class)
		}
		return
	}
	if h.calls != 1 {
		t.Errorf("handler ran %d times, want 1: a %s answer is stored and replayed", h.calls, e.Class)
	}
	if !replayed(second) {
		t.Errorf("the replay does not carry %s: true", idempotencyReplayedHeader)
	}
	if second.Body.String() != first.Body.String() {
		t.Errorf("replayed body %q differs from the first answer %q", second.Body.String(), first.Body.String())
	}
}

// FINAL: the answer is a function of the request alone. Same key + same body replays the same
// answer with Idempotency-Replayed: true and the handler ran once; the same key with another body
// is still the reuse conflict.
func TestFinalRefusalIsStoredAndReplayed(t *testing.T) {
	a := newReplayAdapter(t)
	e := firstOf(t, a, classFinal)
	if e.Slug == "" {
		t.Fatal("the catalogue has no FINAL entry below 500: classify the validation / malformed-body problem types")
	}
	h := &fakeHandler{script: []answerFn{refusing(a, e, e.Slug)}}
	mw := a.Middleware(t, h)

	first := post(t, mw, "k-final", `{"n":1}`)
	again := post(t, mw, "k-final", `{"n":1}`)
	if again.Code != first.Code || again.Body.String() != first.Body.String() {
		t.Errorf("replay = %d %q, want %d %q", again.Code, again.Body.String(), first.Code, first.Body.String())
	}
	if !replayed(again) || replayed(first) {
		t.Errorf("replayed header: first=%v replay=%v, want false/true", replayed(first), replayed(again))
	}
	if h.calls != 1 {
		t.Errorf("handler ran %d times, want 1", h.calls)
	}
	other := post(t, mw, "k-final", `{"n":2}`)
	if other.Code != http.StatusConflict || !strings.Contains(other.Body.String(), keyReusedSlug) {
		t.Errorf("another body under a stored key = %d %q, want 409 %s", other.Code, other.Body.String(), keyReusedSlug)
	}
	if h.calls != 1 {
		t.Errorf("handler ran %d times after the reuse conflict, want 1", h.calls)
	}
}

// OUTCOME: the command committed state and then refused. The writes commit WITH the stored
// answer (one transaction), the same key replays the answer, and a replay records nothing new.
func TestOutcomeRefusalCommitsItsWritesOnceAndIsReplayed(t *testing.T) {
	a := newReplayAdapter(t)
	e := firstOf(t, a, classOutcome)
	if e.Slug == "" {
		t.Skip("the catalogue has no OUTCOME entry: nothing to prove (add this test's case with the first one)")
	}
	h := &fakeHandler{script: []answerFn{refusing(a, e, "outcome")}}
	mw := a.Middleware(t, h)

	first := post(t, mw, "k-outcome", `{"n":1}`)
	if first.Code != e.Status {
		t.Fatalf("status = %d, want %d", first.Code, e.Status)
	}
	if got := a.Committed(t, "outcome"); got != 1 {
		t.Fatalf("committed writes after the refusal = %d, want 1: an OUTCOME refusal commits what the command did", got)
	}
	if got := a.KeyRows(t, "k-outcome"); got != 1 {
		t.Errorf("key rows = %d, want 1: the answer is stored in the same transaction as the writes", got)
	}
	for i := 0; i < 3; i++ {
		rep := post(t, mw, "k-outcome", `{"n":1}`)
		if rep.Code != e.Status || !replayed(rep) || rep.Body.String() != first.Body.String() {
			t.Errorf("replay %d = %d replayed=%v %q, want the stored %d answer", i, rep.Code, replayed(rep), rep.Body.String(), e.Status)
		}
	}
	if h.calls != 1 {
		t.Errorf("handler ran %d times, want 1", h.calls)
	}
	if got := a.Committed(t, "outcome"); got != 1 {
		t.Errorf("committed writes after 3 replays = %d, want 1: a replay records nothing new", got)
	}
}

// RETRYABLE: nothing committed, the answer depends on state that can change. The key is released
// (no stored row); the same request under the same key executes AGAIN and, once the state allows
// it, succeeds and is then stored; a different body under the same key is a NEW request.
func TestRetryableRefusalReleasesTheKeySoTheRetryExecutes(t *testing.T) {
	a := newReplayAdapter(t)
	e := firstOf(t, a, classRetryable)
	if e.Slug == "" {
		t.Fatal("the catalogue has no RETRYABLE entry below 500: classify the optimistic-lock conflict and the not-ready problem types")
	}
	h := &fakeHandler{script: []answerFn{refusing(a, e, "x"), succeeding(a, "ok")}}
	mw := a.Middleware(t, h)

	refused := post(t, mw, "k-retry", `{"n":1}`)
	if refused.Code != e.Status {
		t.Fatalf("first status = %d, want %d", refused.Code, e.Status)
	}
	if got := a.KeyRows(t, "k-retry"); got != 0 {
		t.Fatalf("key rows after a RETRYABLE refusal = %d, want 0: the key must be released", got)
	}
	retry := post(t, mw, "k-retry", `{"n":1}`)
	if h.calls != 2 {
		t.Fatalf("handler ran %d times, want 2: the retry under the same key executes again", h.calls)
	}
	if retry.Code != http.StatusCreated || replayed(retry) {
		t.Errorf("retry = %d replayed=%v, want a fresh 201 (the state changed)", retry.Code, replayed(retry))
	}
	if got := a.KeyRows(t, "k-retry"); got != 1 {
		t.Errorf("key rows after the success = %d, want 1: a 2xx is stored", got)
	}
	if rep := post(t, mw, "k-retry", `{"n":1}`); rep.Code != http.StatusCreated || !replayed(rep) || h.calls != 2 {
		t.Errorf("third request = %d replayed=%v calls=%d, want the stored 201 replayed", rep.Code, replayed(rep), h.calls)
	}
}

func TestRetryableRefusalLeavesTheKeyFreeForAnotherRequest(t *testing.T) {
	a := newReplayAdapter(t)
	e := firstOf(t, a, classRetryable)
	if e.Slug == "" {
		t.Fatal("the catalogue has no RETRYABLE entry below 500")
	}
	h := &fakeHandler{script: []answerFn{refusing(a, e, "x"), succeeding(a, "ok")}}
	mw := a.Middleware(t, h)

	post(t, mw, "k-free", `{"n":1}`)
	other := post(t, mw, "k-free", `{"n":2}`)
	if strings.Contains(other.Body.String(), keyReusedSlug) {
		t.Fatalf("a different body under a released key answered %s: the key must be free, the request is new", keyReusedSlug)
	}
	if h.calls != 2 || other.Code != http.StatusCreated {
		t.Errorf("different body under the released key = %d calls=%d, want a new request: 201 and 2 calls", other.Code, h.calls)
	}
}

// (c) A 2xx is stored and replayed.
func TestSuccessIsStoredAndReplayed(t *testing.T) {
	a := newReplayAdapter(t)
	h := &fakeHandler{script: []answerFn{succeeding(a, "ok")}}
	mw := a.Middleware(t, h)

	first := post(t, mw, "k-2xx", `{"n":1}`)
	rep := post(t, mw, "k-2xx", `{"n":1}`)
	if first.Code != http.StatusCreated || replayed(first) {
		t.Fatalf("first = %d replayed=%v, want a fresh 201", first.Code, replayed(first))
	}
	if rep.Code != http.StatusCreated || !replayed(rep) || rep.Body.String() != first.Body.String() {
		t.Errorf("replay = %d replayed=%v %q, want the stored 201", rep.Code, replayed(rep), rep.Body.String())
	}
	if h.calls != 1 || a.Committed(t, "ok") != 1 || a.KeyRows(t, "k-2xx") != 1 {
		t.Errorf("calls=%d committed=%d rows=%d, want 1/1/1", h.calls, a.Committed(t, "ok"), a.KeyRows(t, "k-2xx"))
	}
}

// (c) A 5xx is never stored: nothing is remembered under the key and the retry executes.
func TestServerErrorIsNotStored(t *testing.T) {
	a := newReplayAdapter(t)
	boom := func(w http.ResponseWriter, r *http.Request) {
		_ = a.Write(r.Context(), "boom")
		w.WriteHeader(http.StatusInternalServerError)
	}
	h := &fakeHandler{script: []answerFn{boom, succeeding(a, "ok")}}
	mw := a.Middleware(t, h)

	if first := post(t, mw, "k-5xx", `{"n":1}`); first.Code < http.StatusInternalServerError {
		t.Fatalf("first = %d, want a 5xx", first.Code)
	}
	if got := a.KeyRows(t, "k-5xx"); got != 0 {
		t.Errorf("key rows after a 5xx = %d, want 0", got)
	}
	retry := post(t, mw, "k-5xx", `{"n":1}`)
	if h.calls != 2 || retry.Code != http.StatusCreated || replayed(retry) {
		t.Errorf("retry = %d replayed=%v calls=%d, want a fresh 201 after 2 calls", retry.Code, replayed(retry), h.calls)
	}
}

// (c) A panic is never stored either. The middleware may re-panic (net/http recovers) or answer
// 500; both are fine, what is not fine is a stored row.
func TestPanicIsNotStored(t *testing.T) {
	a := newReplayAdapter(t)
	crash := func(http.ResponseWriter, *http.Request) { panic("handler panic") }
	h := &fakeHandler{script: []answerFn{crash, succeeding(a, "ok")}}
	mw := a.Middleware(t, h)

	func() {
		defer func() { _ = recover() }()
		post(t, mw, "k-panic", `{"n":1}`)
	}()
	if got := a.KeyRows(t, "k-panic"); got != 0 {
		t.Errorf("key rows after a panic = %d, want 0", got)
	}
	if retry := post(t, mw, "k-panic", `{"n":1}`); h.calls != 2 || retry.Code != http.StatusCreated {
		t.Errorf("retry = %d calls=%d, want a fresh 201 after 2 calls", retry.Code, h.calls)
	}
}

// The key-in-transaction design over the in-memory doubles: the middleware's unit of work is the
// memory store's, Write is a scratch row in that unit of work, and a rollback restores the whole
// store, key row included (docs/idempotency-replay-classes.md, "Stores behind a port").
type repoAdapter struct {
	db   *memory.Store
	idem *memory.Idempotency
}

func newReplayAdapter(t *testing.T) replayAdapter {
	t.Helper()
	db := memory.NewStore()
	return &repoAdapter{db: db, idem: memory.NewIdempotency(db)}
}

func (a *repoAdapter) Catalogue() []problemEntry {
	var out []problemEntry
	for _, e := range ahttp.Catalogue() {
		out = append(out, problemEntry{Slug: e.Slug, Status: e.Status, Class: e.Class})
	}
	return out
}

func (a *repoAdapter) Middleware(_ *testing.T, next http.Handler) http.Handler {
	clock := memory.NewClock(time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC))
	return ahttp.NewIdempotency(a.idem, memory.NewUnitOfWork(a.db), clock).Middleware(next)
}

func (a *repoAdapter) Refuse(w http.ResponseWriter, r *http.Request, e problemEntry) {
	ahttp.Refuse(w, r, e.Slug)
}

func (a *repoAdapter) Write(ctx context.Context, tag string) error { return a.db.Tag(ctx, tag) }

func (a *repoAdapter) Committed(_ *testing.T, tag string) int { return a.db.Tagged(tag) }

func (a *repoAdapter) KeyRows(_ *testing.T, key string) int { return a.idem.Rows(key) }

// --- material-flow-control: the three classes on the relationship-1 routes (plan 04 Task 18). ---
//
// The template tests above drive the middleware with a fake handler; the tests below drive the REAL
// router, middleware and use cases over the memory doubles, once per class: FINAL (invalid-parameter),
// OUTCOME (unknown-container: the rejection record and TransportRejected commit with the stored answer),
// RETRYABLE (transport-ended on the cancellation of a rejected id), a stored 2xx, and a 5xx (a panicking
// handler added through export_test.go) that is never stored; then the 24-hour window.

// fixture is newAPI (handlers_test.go) keeping the clock, the events and the idempotency double.
type fixture struct {
	t      *testing.T
	svc    *usecases.Service
	clock  *memory.Clock
	events *memory.Events
	tracks *memory.Tracks
	orders *memory.Orders
	idem   *memory.Idempotency
	h      http.Handler
}

func newFixture(t *testing.T) *fixture { return newFixtureWith(t, fixtureWiring{}) }

// fixtureWiring wraps the unit of work, the flow lock and the idempotency store (nil: the plain memory ones).
type fixtureWiring struct {
	uow   func(ports.UnitOfWork) ports.UnitOfWork
	lock  func(ports.FlowLock) ports.FlowLock
	store func(ports.IdempotencyStore) ports.IdempotencyStore
}

func newFixtureWith(t *testing.T, w fixtureWiring) *fixture {
	t.Helper()
	l, err := config.LoadLayout("../../../../config/flow-layout.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	s := memory.NewStore()
	f := &fixture{t: t, clock: memory.NewClock(t0), events: memory.NewEvents(s), tracks: memory.NewTracks(s),
		orders: memory.NewOrders(s), idem: memory.NewIdempotency(s)}
	var uow ports.UnitOfWork = memory.NewUnitOfWork(s)
	var lock ports.FlowLock = memory.FlowLock{}
	var store ports.IdempotencyStore = f.idem
	if w.uow != nil {
		uow = w.uow(uow)
	}
	if w.lock != nil {
		lock = w.lock(lock)
	}
	if w.store != nil {
		store = w.store(store)
	}
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))
	f.svc, err = usecases.NewService(l, usecases.DefaultConfig(), usecases.Deps{UoW: uow, Lock: lock,
		Requests: memory.NewRequests(s), Orders: f.orders, Tracks: f.tracks, Stations: memory.NewStations(s),
		Capacities: memory.NewCapacities(s), Units: memory.NewUnits(s), Commands: memory.NewCommands(s), Inbox: memory.NewInbox(s),
		Sink: f.events, Asrs: memory.NewAsrs(), Amr: memory.NewAmr(), Clock: f.clock, IDs: memory.NewIDs("to"),
		Metrics: &memory.Metrics{}, Kick: &memory.Kicker{}, Log: discard})
	if err != nil {
		t.Fatal(err)
	}
	mw := ahttp.NewIdempotency(store, uow, f.clock).Middleware
	f.h = ahttp.WithPanicRoute(ahttp.NewRouter(&ahttp.Server{Service: f.svc, Idempotency: mw}, discard), mw)
	return f
}

func (f *fixture) do(path, key string, body any) (*httptest.ResponseRecorder, map[string]any) {
	f.t.Helper()
	var rd io.Reader = http.NoBody
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req := httptest.NewRequest(http.MethodPost, path, rd)
	req.Header.Set(idempotencyKeyHeader, key)
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec, out
}

func (f *fixture) do2(method, path string) (*httptest.ResponseRecorder, map[string]any) {
	f.t.Helper()
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, httptest.NewRequest(method, path, http.NoBody))
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec, out
}

func (f *fixture) eventTypes() []string {
	out := []string{}
	for _, e := range f.events.All() {
		out = append(out, fmt.Sprintf("%T", e))
	}
	return out
}

type classCase struct {
	name   string
	setup  func(f *fixture)
	path   string
	body   any
	status int
	slug   string
	stored bool
}

func classCases() []classCase {
	rejectFirst := func(f *fixture) { f.do(route, "k-setup", pick("task-9.1", "TOTE-9")) }
	storeTote := func(f *fixture) { f.stored("TOTE-1") }
	return []classCase{
		{"FINAL/invalid-parameter", nil, route, map[string]any{"transport_request_id": "t-2"}, http.StatusBadRequest, "invalid-parameter", true},
		{"OUTCOME/unknown-container", nil, route, pick("task-9.1", "TOTE-9"), http.StatusUnprocessableEntity, "unknown-container", true},
		{"RETRYABLE/transport-ended", rejectFirst, route + "/task-9.1/cancellation", nil, http.StatusConflict, "transport-ended", false},
		{"2xx/accepted", storeTote, route, pick("task-1.1", "TOTE-1"), http.StatusAccepted, "", true},
		{"5xx/panic", nil, ahttp.PanicPath, map[string]any{}, http.StatusInternalServerError, "internal-error", false},
	}
}

func (f *fixture) stored(container string) {
	f.t.Helper()
	(&api{t: f.t, tracks: f.tracks}).stored(container)
}

func TestEachReplayClassOnTheTransportRequestRoutes(t *testing.T) {
	for _, c := range classCases() {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			if c.setup != nil {
				c.setup(f)
			}
			before := len(f.events.All())
			first := f.checkClassFirst(c)
			written := len(f.events.All())
			f.checkClassSecond(c, first, written)
			if c.slug == "unknown-container" && (written-before != 1 || f.eventTypes()[0] != "transportrequest.TransportRejected") {
				t.Fatalf("OUTCOME events = %v, want exactly one TransportRejected committed with the answer", f.eventTypes())
			}
		})
	}
}

func (f *fixture) checkClassFirst(c classCase) *httptest.ResponseRecorder {
	f.t.Helper()
	first, body := f.do(c.path, "k-class", c.body)
	if first.Code != c.status || (c.slug != "" && body["code"] != c.slug) || replayed(first) {
		f.t.Fatalf("first = %d %v replayed=%v, want %d %s fresh", first.Code, body, replayed(first), c.status, c.slug)
	}
	wantRows := 0
	if c.stored {
		wantRows = 1
	}
	if got := f.idem.Rows("k-class"); got != wantRows {
		f.t.Fatalf("key rows after the first answer = %d, want %d", got, wantRows)
	}
	return first
}

func (f *fixture) checkClassSecond(c classCase, first *httptest.ResponseRecorder, written int) {
	f.t.Helper()
	second, _ := f.do(c.path, "k-class", c.body)
	if second.Code != c.status || replayed(second) != c.stored {
		f.t.Fatalf("second = %d replayed=%v, want %d replayed=%v", second.Code, replayed(second), c.status, c.stored)
	}
	if c.stored && second.Body.String() != first.Body.String() {
		f.t.Fatalf("replayed body %q differs from the first %q", second.Body.String(), first.Body.String())
	}
	if got := len(f.events.All()); got != written {
		f.t.Fatalf("events after the second request = %d, want %d (%v): a replay or a retry records nothing new", got, written, f.eventTypes())
	}
}

func TestAnAnswerOlderThanTheWindowIsNotReplayed(t *testing.T) {
	if ahttp.IdempotencyWindow != 24*time.Hour {
		t.Fatalf("IdempotencyWindow = %s, want 24h (fleet rule)", ahttp.IdempotencyWindow)
	}
	f := newFixture(t)
	first, _ := f.do(route, "k-age", pick("task-9.1", "TOTE-9"))
	if first.Code != http.StatusUnprocessableEntity || replayed(first) {
		t.Fatalf("first = %d replayed=%v", first.Code, replayed(first))
	}
	f.clock.Advance(23 * time.Hour)
	if within, _ := f.do(route, "k-age", pick("task-9.1", "TOTE-9")); !replayed(within) {
		t.Fatalf("23 h later = %d replayed=%v, want the stored answer", within.Code, replayed(within))
	}
	f.clock.Advance(2 * time.Hour)
	late, body := f.do(route, "k-age", pick("task-9.1", "TOTE-9"))
	if replayed(late) || late.Code != http.StatusUnprocessableEntity || body["reason"] != "UNKNOWN_CONTAINER" {
		t.Fatalf("25 h later = %d replayed=%v %v, want a fresh 422 (the domain key keeps the rejection, D7)", late.Code, replayed(late), body)
	}
	if n := len(f.events.All()); n != 1 {
		t.Fatalf("events = %v, want the one TransportRejected (a rejected id stays rejected)", f.eventTypes())
	}
	if again, _ := f.do(route, "k-age", pick("task-9.1", "TOTE-9")); !replayed(again) || f.idem.Rows("k-age") != 1 {
		t.Fatalf("after the window the fresh answer is stored again: replayed=%v rows=%d", replayed(again), f.idem.Rows("k-age"))
	}
}
