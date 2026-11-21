package asrs_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/IQVO/material-flow-control/internal/application/subsystem"
)

// carry (Task 8 review): asrs-control's openapi (d40e1f6, unchanged since 007bf7b) and its errors.go class
// job-not-cancellable and job-ended RETRYABLE (409); the real client must agree with the memory fake.
func assertACancelRefusalIsRetryable(t *testing.T, code string) {
	t.Helper()
	c, calls := fake(t, http.StatusConflict, `{"status":409,"code":"`+code+`"}`)
	_, err := c.CancelStorageJob(context.Background(), "cancel-r", "ASRS1", "sj-1")
	var r *subsystem.Refusal
	if !errors.As(err, &r) || r.Status != http.StatusConflict || r.Code != code || !r.Retryable {
		t.Fatalf("%s = %v", code, err)
	}
	if got := (*calls)[0]; got.Path != "/asrs/ASRS1/storage-jobs/sj-1/cancellation" || got.Key != "cancel-r" {
		t.Fatalf("cancel call = %+v", got)
	}
}

// carry (Task 8 review)
func TestJobNotCancellableIsRetryable(t *testing.T) {
	assertACancelRefusalIsRetryable(t, "job-not-cancellable")
}

// carry (Task 8 review)
func TestJobEndedIsRetryable(t *testing.T) { assertACancelRefusalIsRetryable(t, "job-ended") }
