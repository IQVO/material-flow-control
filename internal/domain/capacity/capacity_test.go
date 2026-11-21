package capacity_test

import (
	"testing"
	"time"

	"github.com/IQVO/material-flow-control/internal/domain/capacity"
)

var t0 = time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)

func TestAnAsrsIsUnavailableWithoutAUnitAndDegradedWithFewerThanAll(t *testing.T) {
	cases := []struct {
		total, available int
		status           capacity.Status
		perHour          int
	}{
		{2, 2, capacity.StatusAvailable, 120},
		{2, 1, capacity.StatusDegraded, 60},
		{2, 0, capacity.StatusUnavailable, 0},
	}
	for _, c := range cases {
		s := capacity.Asrs("ASRS1", c.total, c.available, 3, 60, t0)
		want := capacity.Snapshot{Kind: capacity.KindAsrs, ID: "ASRS1", Status: c.status, UnitsTotal: c.total, UnitsAvailable: c.available,
			OpenWork: 3, CapacityPerHour: c.perHour, MeasuredAt: t0}
		if s != want {
			t.Fatalf("Asrs(%d/%d) = %+v, want %+v", c.available, c.total, s, want)
		}
	}
	if s := capacity.Asrs("ASRS1", 2, -1, 0, 60, t0); s.CapacityPerHour != 0 || s.UnitsAvailable != 0 || s.Status != capacity.StatusUnavailable {
		t.Fatalf("a negative count = %+v", s)
	}
	if got := capacity.Asrs("ASRS1", 2, 2, 0, 60, t0).Subject(); got != "ASRS/ASRS1" {
		t.Fatalf("subject = %s", got)
	}
}

func TestAFleetIsUnavailableOnlyWhenNoRobotIsOnline(t *testing.T) {
	half := capacity.Fleet("AMR1", 3, 1, 1, 2, 1, 30, t0)
	want := capacity.Snapshot{Kind: capacity.KindAmrFleet, ID: "AMR1", Status: capacity.StatusDegraded, UnitsTotal: 3, UnitsAvailable: 1,
		OpenWork: 3, CapacityPerHour: 30, MeasuredAt: t0}
	if half != want {
		t.Fatalf("Fleet = %+v", half)
	}
	if s := capacity.Fleet("AMR1", 3, 3, 0, 5, 3, 30, t0); s.Status != capacity.StatusDegraded {
		t.Fatalf("online but all busy = %s (legs queue, so the fleet is not unavailable)", s.Status)
	}
	if s := capacity.Fleet("AMR1", 3, 0, 0, 0, 0, 30, t0); s.Status != capacity.StatusUnavailable {
		t.Fatalf("no robot online = %s", s.Status)
	}
	if s := capacity.Fleet("AMR1", 3, 3, 3, 0, 0, 30, t0); s.Status != capacity.StatusAvailable || s.CapacityPerHour != 90 || s.Subject() != "AMR_FLEET/AMR1" {
		t.Fatalf("all available = %+v", s)
	}
}

func TestAnEmptyFleetOrAsrsIsAcceptedAndUnavailable(t *testing.T) {
	empty := capacity.Fleet("AMR1", 0, 0, 0, 0, 0, 30, t0)
	want := capacity.Snapshot{Kind: capacity.KindAmrFleet, ID: "AMR1", Status: capacity.StatusUnavailable, MeasuredAt: t0}
	if empty != want {
		t.Fatalf("robots_total 0 = %+v, want %+v", empty, want)
	}
	noUnits := capacity.Asrs("ASRS1", 0, 0, 0, 60, t0)
	if noUnits.Status != capacity.StatusUnavailable || noUnits.UnitsTotal != 0 || noUnits.CapacityPerHour != 0 {
		t.Fatalf("units_total 0 = %+v", noUnits)
	}
	if s := capacity.Fleet("AMR1", 2, 2, 0, -1, 4, 30, t0); s.OpenWork != 4 || s.Status != capacity.StatusDegraded {
		t.Fatalf("open work = queued + active, a negative count clamps: %+v", s)
	}
}

func TestAStationReportsItsBufferAndItsBacklog(t *testing.T) {
	s := capacity.Station("ST1", 2, 2, 3, t0)
	want := capacity.Snapshot{Kind: capacity.KindStation, ID: "ST1", Status: capacity.StatusDegraded, UnitsTotal: 2, UnitsAvailable: 0,
		OpenWork: 5, BufferLimit: 2, Occupied: 2, QueueDepth: 3, MeasuredAt: t0}
	if s != want {
		t.Fatalf("Station = %+v", s)
	}
	if s := capacity.Station("ST1", 2, 1, 0, t0); s.Status != capacity.StatusAvailable || s.UnitsAvailable != 1 {
		t.Fatalf("free slot = %+v", s)
	}
	if s := capacity.Station("ST1", 1, 2, 0, t0); s.UnitsAvailable != 0 {
		t.Fatalf("over a lowered buffer = %+v", s)
	}
}

func TestOnlyAChangeOfAPublishedFieldIsPublished(t *testing.T) {
	prev := capacity.Asrs("ASRS1", 2, 2, 0, 60, t0)
	if _, ok := capacity.Changed(prev, true, capacity.Asrs("ASRS1", 2, 2, 0, 60, t0.Add(time.Minute))); ok {
		t.Fatal("a new measurement time alone published")
	}
	ev, ok := capacity.Changed(prev, true, capacity.Asrs("ASRS1", 2, 1, 0, 60, t0.Add(time.Minute)))
	if !ok || ev.UnitsAvailable != 1 || !ev.MeasuredAt.Equal(t0.Add(time.Minute)) {
		t.Fatalf("a drop = %+v %v", ev, ok)
	}
	if _, ok := capacity.Changed(capacity.Snapshot{}, false, prev); !ok {
		t.Fatal("the first snapshot is always published")
	}
	if !prev.SameFacts(capacity.Asrs("ASRS1", 2, 2, 0, 60, t0.Add(time.Hour))) || prev.SameFacts(capacity.Asrs("ASRS1", 2, 2, 1, 60, t0)) {
		t.Fatal("SameFacts")
	}
}

// carry (Task 7 review): every published count clamps to 0 (D17), copied and never recomputed.
func TestEveryPublishedCountClampsToZero(t *testing.T) {
	fleet := capacity.Fleet("F", -1, 1, 0, 0, 0, 30, t0)
	wantFleet := capacity.Snapshot{Kind: capacity.KindAmrFleet, ID: "F", Status: capacity.StatusAvailable, MeasuredAt: t0}
	if fleet != wantFleet {
		t.Errorf("a negative robots_total with a robot online = %+v, want %+v (available 0 of 0)", fleet, wantFleet)
	}
	if s := capacity.Asrs("A", -2, 0, 0, 60, t0); s.UnitsTotal != 0 || s.Status != capacity.StatusUnavailable {
		t.Errorf("a negative units_total = %+v", s)
	}
	station := capacity.Station("S", -1, -2, -3, t0)
	wantStation := capacity.Snapshot{Kind: capacity.KindStation, ID: "S", Status: capacity.StatusAvailable, MeasuredAt: t0}
	if station != wantStation {
		t.Errorf("negative station counts = %+v, want %+v", station, wantStation)
	}
}
