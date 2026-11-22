//go:build integration

package main

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/IQVO/material-flow-control/internal/testing/pgtest"
)

// carry (Task 26 review): wire() hands the SAME catch-up gate to the POST routes and to the leg-timeout tick (ruling
// P4-R20). With a broker that never answers the gate is never sampled: app.caughtUp is set and errs, a POST is 503,
// and the tick loop runs no tick (review mutant mD: wire drops app.caughtUp, so the tick is ungated).
func TestWireGatesThePostsAndTheTickOnTheCatchUp(t *testing.T) {
	layout, _ := filepath.Abs("testdata/catchup/flow-layout.yaml")
	cfg, err := loadConfig(env(map[string]string{"DATABASE_URL": pgtest.URL(t), "KAFKA_BROKERS": "127.0.0.1:1",
		"ASRS_CONTROL_URL": "http://127.0.0.1:1", "AMR_FLEET_CONTROL_URL": "http://127.0.0.1:1", "MFC_LAYOUT_FILE": layout}))
	if err != nil {
		t.Fatal(err)
	}
	cfg.TickInterval = time.Millisecond
	a, err := wire(context.Background(), cfg, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, c := range a.closers {
			c()
		}
	})
	if a.caughtUp == nil {
		t.Fatal("wire left app.caughtUp nil: the tick is not gated on the catch-up")
	}
	if err := a.caughtUp(); err == nil {
		t.Fatal("app.caughtUp() = nil before the catch-up ran, want an error")
	}
	req := httptest.NewRequest(http.MethodPost, "/transport-requests", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Idempotency-Key", "k-wiring")
	rec := httptest.NewRecorder()
	a.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("POST /transport-requests before the catch-up = %d %s, want 503", rec.Code, rec.Body)
	}
	var ticks atomic.Int32
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	a.tickLoop(ctx, func(context.Context) (int, error) { ticks.Add(1); return 0, nil })
	if n := ticks.Load(); n != 0 {
		t.Fatalf("the wired tick loop ran %d ticks before the catch-up, want 0", n)
	}
}
