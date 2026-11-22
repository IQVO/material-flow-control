package command_test

import (
	"testing"
	"time"

	"github.com/IQVO/material-flow-control/internal/application/command"
)

var t0 = time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)

func TestKeysAreStablePerKindAndLegAttempt(t *testing.T) {
	cases := map[command.Kind]string{command.RequestLeg: "to-1.1.1", command.CancelLeg: "cancel-to-1.1.1", command.ConfirmPickup: "pickup-to-1.1.1"}
	for kind, want := range cases {
		c := command.New("c1", "to-1", "to-1.1.1", kind, t0, 300*time.Second)
		if c.Key != want || c.Status != command.StatusPending || !c.NextAttemptAt.Equal(t0) || !c.Deadline.Equal(t0.Add(300*time.Second)) {
			t.Fatalf("%s = %+v", kind, c)
		}
	}
}

func TestBackoffDoublesAndCaps(t *testing.T) {
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 30 * time.Second, 30 * time.Second}
	for i, w := range want {
		if got := command.Backoff(i+1, time.Second, 30*time.Second); got != w {
			t.Fatalf("Backoff(%d) = %s, want %s", i+1, got, w)
		}
	}
}

func TestRetryDoneFailedAndExpiry(t *testing.T) {
	c := command.New("c1", "to-1", "r", command.RequestLeg, t0, 10*time.Second)
	r := c.Retry(t0, time.Second, 30*time.Second, "boom")
	if r.Attempts != 1 || !r.NextAttemptAt.Equal(t0.Add(time.Second)) || r.LastError != "boom" || r.Status != command.StatusPending {
		t.Fatalf("retry = %+v", r)
	}
	if r2 := r.Retry(t0, time.Second, 30*time.Second, "x"); !r2.NextAttemptAt.Equal(t0.Add(2 * time.Second)) {
		t.Fatalf("second retry = %+v", r2)
	}
	if c.Expired(t0.Add(9*time.Second)) || !c.Expired(t0.Add(10*time.Second)) {
		t.Fatal("Expired is inclusive of the deadline")
	}
	if d := c.Done(t0); d.Status != command.StatusDone || d.Attempts != 1 {
		t.Fatalf("done = %+v", d)
	}
	if f := c.Failed(t0, "gone"); f.Status != command.StatusFailed || f.LastError != "gone" {
		t.Fatalf("failed = %+v", f)
	}
}
