package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	ahttp "github.com/IQVO/material-flow-control/internal/adapters/inbound/http"
	"github.com/IQVO/material-flow-control/internal/adapters/outbound/memory"
	"github.com/IQVO/material-flow-control/internal/application/idempotency"
	"github.com/IQVO/material-flow-control/internal/application/ports"
)

// rereadFailingStore answers the middleware's first Lookup from the store and fails every later one, the way a
// database that dropped between the request and the unstored refusal's re-read does.
type rereadFailingStore struct {
	ports.IdempotencyStore
	mu      sync.Mutex
	lookups int
}

func (s *rereadFailingStore) Lookup(ctx context.Context, key string, notBefore time.Time) (idempotency.IdempotencyRecord, bool, error) {
	s.mu.Lock()
	s.lookups++
	n := s.lookups
	s.mu.Unlock()
	if n > 1 {
		return idempotency.IdempotencyRecord{}, false, errors.New("idempotency store unreachable")
	}
	return s.IdempotencyStore.Lookup(ctx, key, notBefore)
}

// carry (Task 19 review): a failed re-read before an unstored refusal is logged at WARN, and the request's own
// refusal still goes out (the key stays released).
func TestAFailedReReadBeforeAnUnstoredRefusalIsLoggedAndTheRefusalAnswered(t *testing.T) {
	s := memory.NewStore()
	store := &rereadFailingStore{IdempotencyStore: memory.NewIdempotency(s)}
	var logs bytes.Buffer
	m := ahttp.NewIdempotency(store, memory.NewUnitOfWork(s), memory.NewClock(t0))
	m.Logger = slog.New(slog.NewJSONHandler(&logs, nil))
	h := m.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"code":"subsystem-unavailable"}`))
	}))
	req := httptest.NewRequest(http.MethodPost, "/transport-requests/rq-1/cancellation", http.NoBody)
	req.Header.Set(idempotencyKeyHeader, "k-1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable || rec.Body.String() != `{"code":"subsystem-unavailable"}` || replayed(rec) {
		t.Fatalf("answer = %d %s replayed=%v, want the request's own 503", rec.Code, rec.Body.String(), replayed(rec))
	}
	if store.lookups != 2 {
		t.Fatalf("lookups = %d, want the first lookup and the re-read", store.lookups)
	}
	var entry map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(logs.Bytes()), &entry); err != nil {
		t.Fatalf("log = %q, want one JSON line: %v", logs.String(), err)
	}
	if entry["level"] != "WARN" || entry["msg"] != "idempotency: re-read before an unstored refusal failed" ||
		!strings.Contains(entry["error"].(string), "idempotency store unreachable") {
		t.Fatalf("log entry = %v", entry)
	}
}
