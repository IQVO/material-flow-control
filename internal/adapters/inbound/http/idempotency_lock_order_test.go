package http_test

// carry (Task 13 review): no idempotency write before the use case's flow lock. The Idempotency middleware's
// unit of work is the OUTERMOST one of a POST; the use cases join it and take the flow lock first (D19), and the
// key row is saved only after the handler returned, so no request ever holds a key row while it waits for the
// flow lock (the lock-order inversion that would deadlock two requests into a 500). A concurrent duplicate is
// serialized by the flow lock, loses the key at save time, and its writes roll back (ADR 0005).

import (
	"context"
	"net/http"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/IQVO/material-flow-control/internal/application/idempotency"
	"github.com/IQVO/material-flow-control/internal/application/ports"
)

// opLog records, in order, the outermost unit of work's begin and end, every flow lock and every key-row save.
type opLog struct {
	mu  sync.Mutex
	ops []string
}

func (l *opLog) add(op string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ops = append(l.ops, op)
}

func (l *opLog) take() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := l.ops
	l.ops = nil
	return out
}

type outerUoW struct{}

type loggedUoW struct {
	ports.UnitOfWork
	log *opLog
}

func (u loggedUoW) Do(ctx context.Context, fn func(ctx context.Context) error) error {
	if ctx.Value(outerUoW{}) != nil {
		return u.UnitOfWork.Do(ctx, fn)
	}
	u.log.add("begin")
	defer u.log.add("end")
	return u.UnitOfWork.Do(ctx, func(ctx context.Context) error { return fn(context.WithValue(ctx, outerUoW{}, true)) })
}

type loggedLock struct {
	ports.FlowLock
	log *opLog
}

func (l loggedLock) Lock(ctx context.Context) error {
	l.log.add("lock")
	return l.FlowLock.Lock(ctx)
}

type loggedStore struct {
	ports.IdempotencyStore
	log   *opLog
	blind int // the next blind Lookups find nothing, as when a concurrent duplicate has not committed yet
	mu    sync.Mutex
}

func (s *loggedStore) Save(ctx context.Context, rec idempotency.IdempotencyRecord, notBefore time.Time) error {
	s.log.add("save")
	return s.IdempotencyStore.Save(ctx, rec, notBefore)
}

func (s *loggedStore) Lookup(ctx context.Context, key string, notBefore time.Time) (idempotency.IdempotencyRecord, bool, error) {
	s.mu.Lock()
	blind := s.blind > 0
	if blind {
		s.blind--
	}
	s.mu.Unlock()
	if blind {
		return idempotency.IdempotencyRecord{}, false, nil
	}
	return s.IdempotencyStore.Lookup(ctx, key, notBefore)
}

func loggedFixture(t *testing.T) (*fixture, *opLog, *loggedStore) {
	t.Helper()
	log := &opLog{}
	var st *loggedStore
	f := newFixtureWith(t, fixtureWiring{
		uow:  func(u ports.UnitOfWork) ports.UnitOfWork { return loggedUoW{u, log} },
		lock: func(l ports.FlowLock) ports.FlowLock { return loggedLock{l, log} },
		store: func(s ports.IdempotencyStore) ports.IdempotencyStore {
			st = &loggedStore{IdempotencyStore: s, log: log}
			return st
		},
	})
	return f, log, st
}

// keyWritesBeforeTheLock counts the key-row saves made inside a unit of work before it took the flow lock.
func keyWritesBeforeTheLock(ops []string) int {
	inside, locked, bad := false, false, 0
	for _, op := range ops {
		switch op {
		case "begin":
			inside, locked = true, false
		case "end":
			inside = false
		case "lock":
			locked = true
		case "save":
			if inside && !locked {
				bad++
			}
		}
	}
	return bad
}

func TestTheKeyWriteCheckBites(t *testing.T) {
	if n := keyWritesBeforeTheLock([]string{"begin", "save", "lock", "end"}); n != 1 {
		t.Fatalf("a save before the lock counted %d, want 1", n)
	}
	if n := keyWritesBeforeTheLock([]string{"begin", "lock", "save", "end", "begin", "end", "save"}); n != 0 {
		t.Fatalf("a save after the lock, or outside a unit of work, counted %d, want 0", n)
	}
}

func TestNoKeyRowIsWrittenBeforeTheFlowLock(t *testing.T) {
	f, log, _ := loggedFixture(t)
	f.stored("TOTE-1")
	cases := []struct {
		name, path string
		body       any
		status     int
		want       []string
	}{
		{"2xx stored in the use case's unit of work", route, pick("task-1.1", "TOTE-1"), http.StatusAccepted, []string{"begin", "lock", "save", "end"}},
		{"OUTCOME stored with its writes", route, pick("task-9.1", "TOTE-9"), http.StatusUnprocessableEntity, []string{"begin", "lock", "save", "end"}},
		{"FINAL stored on its own after the rollback", route, map[string]any{"transport_request_id": "t-2"}, http.StatusBadRequest, []string{"begin", "end", "save"}},
		{"RETRYABLE never stored", route + "/task-9.1/cancellation", nil, http.StatusConflict, []string{"begin", "lock", "end"}},
		{"cancellation 202 stored after the lock", route + "/task-1.1/cancellation", nil, http.StatusAccepted, []string{"begin", "lock", "save", "end"}},
		{"resolution refusal never stored", "/container-tracks/TOTE-1/clarification-resolution", nil, http.StatusConflict, []string{"begin", "lock", "end"}},
	}
	for i, c := range cases {
		log.take()
		rec, body := f.do(c.path, "k-order-"+string(rune('a'+i)), c.body)
		ops := log.take()
		if rec.Code != c.status {
			t.Fatalf("%s: status %d %v, want %d", c.name, rec.Code, body, c.status)
		}
		if n := keyWritesBeforeTheLock(ops); n != 0 || !reflect.DeepEqual(ops, c.want) {
			t.Fatalf("%s: ops %v (%d key writes before the flow lock), want %v", c.name, ops, n, c.want)
		}
	}
}

// Two copies of one request under one key, arriving together: both pass the Lookup before either committed
// (the blind Lookup stands for it). The second waits on the flow lock, runs, loses the key at save time and
// rolls back: it answers the winner's answer, and nothing it wrote survives.
func TestAConcurrentDuplicateWithAnotherBodyRollsBackAndAnswersKeyReused(t *testing.T) {
	f, _, st := loggedFixture(t)
	first, _ := f.do(route, "k-race", pick("task-9.1", "TOTE-9"))
	events := len(f.events.All())
	st.blind = 1
	second, body := f.do(route, "k-race", pick("task-9.2", "TOTE-8"))
	if first.Code != http.StatusUnprocessableEntity || second.Code != http.StatusConflict || body["code"] != "idempotency-key-reused" {
		t.Fatalf("first %d, second %d %v, want 422 then 409 idempotency-key-reused", first.Code, second.Code, body)
	}
	if got := len(f.events.All()); got != events {
		t.Fatalf("events = %v, want the first request's %d only: the loser's TransportRejected rolled back", f.eventTypes(), events)
	}
	if get, view := f.do2(http.MethodGet, route+"/task-9.2"); get.Code != http.StatusNotFound {
		t.Fatalf("the loser's record survived: %d %v", get.Code, view)
	}
}

func TestABurstOfOneRequestUnderOneKeyRunsOnce(t *testing.T) {
	for _, c := range []struct {
		name   string
		body   map[string]any
		status int
	}{{"accepted", pick("task-1.1", "TOTE-1"), http.StatusAccepted}, {"rejected", pick("task-9.1", "TOTE-9"), http.StatusUnprocessableEntity}} {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			f.stored("TOTE-1")
			const n = 8
			codes, ids := make([]int, n), make([]any, n)
			var wg sync.WaitGroup
			for i := 0; i < n; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					rec, body := f.do(route, "k-burst", c.body)
					codes[i], ids[i] = rec.Code, body["transport_order_id"]
				}(i)
			}
			wg.Wait()
			for i := range codes {
				if codes[i] != c.status || ids[i] != ids[0] {
					t.Fatalf("answers %v ids %v, want %d with one order id", codes, ids, c.status)
				}
			}
			if f.idem.Rows("k-burst") != 1 || len(f.events.All()) != 1 {
				t.Fatalf("rows %d events %v, want one key row and one event", f.idem.Rows("k-burst"), f.eventTypes())
			}
		})
	}
}
