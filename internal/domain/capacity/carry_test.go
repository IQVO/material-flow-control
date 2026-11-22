package capacity_test

import (
	"testing"
	"time"

	"github.com/IQVO/material-flow-control/internal/domain/capacity"
)

// carry (Task 7 review): every count clamps to 0 (D17) on every subsystem kind, with the status taken from the
// clamped counts.
func TestEveryNegativeCountClampsToZero(t *testing.T) {
	if s := capacity.Asrs("ASRS1", 2, 2, -3, 60, t0); s.OpenWork != 0 || s.Status != capacity.StatusAvailable {
		t.Fatalf("Asrs openJobs -3 = %+v, want open work 0, AVAILABLE", s)
	}
	if s := capacity.Fleet("AMR1", 3, 3, 3, 1, -4, 30, t0); s.OpenWork != 1 {
		t.Fatalf("Fleet active -4 = %+v, want open work 1 (queued 1 + 0)", s)
	}
	if s := capacity.Fleet("AMR1", 3, 3, -1, 0, 0, 30, t0); s.UnitsAvailable != 0 || s.Status != capacity.StatusDegraded || s.CapacityPerHour != 0 {
		t.Fatalf("Fleet available -1 = %+v, want 0 available, DEGRADED, 0 per hour", s)
	}
	if s := capacity.Fleet("AMR1", 3, 0, -1, 0, 0, 30, t0); s.Status != capacity.StatusUnavailable {
		t.Fatalf("Fleet with no robot online = %+v, want UNAVAILABLE", s)
	}
}

// carry (Task 7 review): SameFacts compares every published field but MeasuredAt; Changed publishes the first
// snapshot even when its facts equal the zero value, and never a snapshot whose facts did not change.
func TestSameFactsComparesEveryFieldButTheMeasurementTime(t *testing.T) {
	base := capacity.Station("ST1", 2, 1, 0, t0)
	if !base.SameFacts(capacity.Station("ST1", 2, 1, 0, t0.Add(time.Hour))) {
		t.Fatal("a new MeasuredAt alone changed the facts")
	}
	for name, other := range map[string]capacity.Snapshot{
		"id":       capacity.Station("ST2", 2, 1, 0, t0),
		"limit":    capacity.Station("ST1", 3, 1, 0, t0),
		"occupied": capacity.Station("ST1", 2, 2, 0, t0),
		"waiting":  capacity.Station("ST1", 2, 1, 1, t0),
		"kind":     capacity.Asrs("ST1", 2, 1, 1, 0, t0),
	} {
		if base.SameFacts(other) {
			t.Fatalf("%s: SameFacts = true for %+v", name, other)
		}
	}
	if _, ok := capacity.Changed(capacity.Snapshot{}, false, capacity.Snapshot{}); !ok {
		t.Fatal("Changed(not found) = false, want the first snapshot published")
	}
	if _, ok := capacity.Changed(base, true, capacity.Station("ST1", 2, 1, 0, t0.Add(time.Minute))); ok {
		t.Fatal("Changed published a snapshot whose facts did not change")
	}
}
