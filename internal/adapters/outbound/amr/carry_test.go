package amr_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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

// carry (Task 14 review): the relationship-4 table answers a Leg (leg_id) on the 202 of RequestLeg and on the
// 200/202 of CancelLeg; a 2xx without it is not an acceptance the dispatcher can track.
func TestALegAnswerWithoutAnIDIsUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{}`)
	}))
	t.Cleanup(srv.Close)
	c := amr.New(srv.URL, time.Second)
	_, err := c.RequestLeg(context.Background(), "k", "AMR1", subsystem.LegRequest{ContainerID: "C", From: "A", To: "B", Reference: "r"})
	if !errors.Is(err, subsystem.ErrUnreachable) || !strings.Contains(err.Error(), "/fleets/AMR1/legs answered no id") {
		t.Fatalf("request = %v", err)
	}
	_, err = c.CancelLeg(context.Background(), "k", "AMR1", "leg-1")
	if !errors.Is(err, subsystem.ErrUnreachable) || !strings.Contains(err.Error(), "/fleets/AMR1/legs/leg-1/cancellation answered no id") {
		t.Fatalf("cancel = %v", err)
	}
}
