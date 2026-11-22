//go:build integration

package http_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	ct "github.com/IQVO/material-flow-control/internal/domain/containertrack"
)

// carry (Task 19 review, INVESTIGATE / P3-R15b): N copies of one keyed request are one run and N-1
// replays, also when the copies arrive while the first is in flight. Every copy passes the key
// Lookup before the first commits and queues on the flow lock (held here by the test, so the race
// is deterministic); the first to get it lifts the hold, and each later copy then decides
// not-in-clarification (409, RETRYABLE, never stored). Without the re-read that copy answered its
// own RETRYABLE refusal instead of replaying the stored 200.
func TestInFlightDuplicatesOfAResolutionReplayItsAnswerOverPostgres(t *testing.T) {
	p := newPostgresAPI(t)
	tr := ct.New("TOTE-3", t0)
	tr.MoveTo(ct.Position{Kind: ct.InAsrs, Code: asrsLoc}, t0)
	tr.SendToClarification(ct.Clarification{Reason: ct.ReasonNoRead, DetectedAt: asrsLoc}, t0)
	p.save(t, tr)
	a := &api{t: t, h: p.h}
	release := holdTheFlowLock(t, p)
	const copies = 3
	recs := make([]*httptest.ResponseRecorder, copies)
	outs := make([]map[string]any, copies)
	var wg sync.WaitGroup
	for i := range copies {
		wg.Go(func() {
			recs[i], outs[i] = a.do(http.MethodPost, "/container-tracks/TOTE-3/clarification-resolution", "k-res-3", nil)
		})
	}
	waitForLockWaiters(t, p, copies)
	release()
	wg.Wait()
	fresh, replays := 0, 0
	for i, rec := range recs {
		if rec.Code != http.StatusOK {
			t.Fatalf("copy %d = %d %v (replayed=%v); answers %v: an in-flight duplicate must replay the first 200", i, rec.Code, outs[i], replayed(rec), codesOf(recs))
		}
		if replayed(rec) {
			replays++
		} else {
			fresh++
		}
	}
	if fresh != 1 || replays != copies-1 {
		t.Fatalf("answers %v: %d fresh, %d replays, want 1 and %d", codesOf(recs), fresh, replays, copies-1)
	}
	if got := p.count(t, `SELECT count(*) FROM idempotency_keys`); got != 1 {
		t.Fatalf("key rows = %d, want 1", got)
	}
}

// holdTheFlowLock takes the flow lock in a unit of work of the test's own and returns its release.
func holdTheFlowLock(t *testing.T, p *pgAPI) func() {
	t.Helper()
	holding, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- p.uow.Do(context.Background(), func(ctx context.Context) error {
			if err := p.lock.Lock(ctx); err != nil {
				return err
			}
			close(holding)
			<-release
			return nil
		})
	}()
	select {
	case <-holding:
	case err := <-done:
		t.Fatalf("holder: %v", err)
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			close(release)
			if err := <-done; err != nil {
				t.Errorf("holder: %v", err)
			}
		})
	}
}

// waitForLockWaiters waits (bounded well under the 5 s lock_timeout) until n sessions wait on a lock.
func waitForLockWaiters(t *testing.T, p *pgAPI, n int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	got := 0
	for time.Now().Before(deadline) {
		got = p.count(t, `SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'`)
		if got >= n {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%d sessions wait on the flow lock after 3 s, want %d", got, n)
}

func codesOf(recs []*httptest.ResponseRecorder) []int {
	out := make([]int, len(recs))
	for i, r := range recs {
		if r != nil {
			out[i] = r.Code
		}
	}
	return out
}
