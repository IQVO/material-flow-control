package amr_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/IQVO/material-flow-control/internal/adapters/outbound/amr"
	"github.com/IQVO/material-flow-control/internal/application/subsystem"
)

// carry (Task 8 review): plan 03's relationship-4 table classes leg-not-cancellable and leg-ended 409 R
// (RETRYABLE); the real client must agree with the memory fake.
func assertACancelRefusalIsRetryable(t *testing.T, code string) {
	t.Helper()
	var path, key string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, key = r.URL.Path, r.Header.Get("Idempotency-Key")
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w, `{"status":409,"code":"`+code+`"}`)
	}))
	t.Cleanup(srv.Close)
	_, err := amr.New(srv.URL, time.Second).CancelLeg(context.Background(), "cancel-to-1.2.1", "AMR1", "leg-1")
	var r *subsystem.Refusal
	if !errors.As(err, &r) || r.Status != http.StatusConflict || r.Code != code || !r.Retryable {
		t.Fatalf("%s = %v", code, err)
	}
	if path != "/fleets/AMR1/legs/leg-1/cancellation" || key != "cancel-to-1.2.1" {
		t.Fatalf("cancel call = %s %s", path, key)
	}
}

// carry (Task 8 review)
func TestLegNotCancellableIsRetryable(t *testing.T) {
	assertACancelRefusalIsRetryable(t, "leg-not-cancellable")
}

// carry (Task 8 review)
func TestLegEndedIsRetryable(t *testing.T) { assertACancelRefusalIsRetryable(t, "leg-ended") }
