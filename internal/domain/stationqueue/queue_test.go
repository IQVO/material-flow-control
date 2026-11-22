package stationqueue_test

import (
	"testing"
	"time"

	sq "github.com/IQVO/material-flow-control/internal/domain/stationqueue"
	tr "github.com/IQVO/material-flow-control/internal/domain/transportrequest"
)

var t0 = time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)

func entry(order string, p tr.Priority, due, ready time.Duration) sq.Entry {
	return sq.Entry{OrderID: order, RequestID: "rq-" + order, ContainerID: "C-" + order, Priority: p, DueBy: t0.Add(due), ReadyAt: t0.Add(ready)}
}

func ids(os []sq.Occupant) []string {
	out := make([]string, len(os))
	for i, o := range os {
		out[i] = o.OrderID
	}
	return out
}

func same(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestArrivalsFollowPriorityThenDueThenReadinessAndNeverExceedTheBuffer is the plan 04 Task 5 brief's test split for
// cyclop (max 15): its statements moved verbatim, in order, into assertAdmitsByPriorityDueReadinessUpToTheBuffer and
// assertADepartureAdmitsTheNextInSequence; every assertion is byte-identical to the brief.
func TestArrivalsFollowPriorityThenDueThenReadinessAndNeverExceedTheBuffer(t *testing.T) {
	q := sq.New("ST1", 2)
	assertAdmitsByPriorityDueReadinessUpToTheBuffer(t, q)
	assertADepartureAdmitsTheNextInSequence(t, q)
}

func assertAdmitsByPriorityDueReadinessUpToTheBuffer(t *testing.T, q *sq.Queue) {
	t.Helper()
	for _, e := range []sq.Entry{
		entry("a", tr.PriorityNormal, 10*time.Minute, 0),
		entry("b", tr.PriorityNormal, 5*time.Minute, time.Second),
		entry("c", tr.PriorityUrgent, 30*time.Minute, 2*time.Second),
		entry("d", tr.PriorityNormal, 5*time.Minute, 0),
	} {
		if !q.Join(e) {
			t.Fatalf("Join(%s) = false", e.OrderID)
		}
	}
	got := q.Admit(t0.Add(time.Minute))
	if !same(ids(got), []string{"c", "d"}) {
		t.Fatalf("admitted = %v, want URGENT c, then d (due 5m, ready first)", ids(got))
	}
	if got[0].Seq != 1 || got[1].Seq != 2 || !got[0].AdmittedAt.Equal(t0.Add(time.Minute)) {
		t.Fatalf("sequence = %+v", got)
	}
	if q.Occupied() != 2 || len(q.Admit(t0)) != 0 {
		t.Fatal("a full station admitted another arrival")
	}
	if w := q.Waiting(); len(w) != 2 || w[0].OrderID != "b" || w[1].OrderID != "a" {
		t.Fatalf("waiting = %+v, want b (due 5m) then a", w)
	}
}

func assertADepartureAdmitsTheNextInSequence(t *testing.T, q *sq.Queue) {
	t.Helper()
	if !q.Leave("C-c") || q.Leave("C-c") {
		t.Fatal("Leave removes the occupant exactly once")
	}
	if next := q.Admit(t0.Add(2 * time.Minute)); !same(ids(next), []string{"b"}) || next[0].Seq != 3 {
		t.Fatalf("after a departure = %+v", next)
	}
	if q.Position("C-d") != 1 || q.Position("C-b") != 2 || q.Position("C-a") != 0 {
		t.Fatalf("positions = %d %d %d", q.Position("C-d"), q.Position("C-b"), q.Position("C-a"))
	}
}

func TestOrderIDBreaksAFullTie(t *testing.T) {
	q := sq.New("ST1", 1)
	q.Join(entry("z", tr.PriorityNormal, 0, 0))
	q.Join(entry("y", tr.PriorityNormal, 0, 0))
	if got := q.Admit(t0); !same(ids(got), []string{"y"}) {
		t.Fatalf("admitted = %v", ids(got))
	}
}

func TestJoinIsIdempotentAndDropRemovesWaitingOrAdmitted(t *testing.T) {
	q := sq.New("ST1", 1)
	q.Join(entry("a", tr.PriorityNormal, 0, 0))
	if q.Join(entry("a", tr.PriorityUrgent, 0, 0)) {
		t.Fatal("a second Join of one order")
	}
	q.Admit(t0)
	if q.Join(entry("a", tr.PriorityNormal, 0, 0)) {
		t.Fatal("an admitted order joined again")
	}
	q.Join(entry("b", tr.PriorityNormal, 0, 0))
	if !q.Drop("b") || len(q.Waiting()) != 0 {
		t.Fatal("Drop of a waiting entry")
	}
	if !q.Drop("a") || q.Occupied() != 0 || q.Drop("a") {
		t.Fatal("Drop of an admitted order frees its slot once")
	}
}

func TestALowerBufferStopsAdmissionsUntilOccupancyFallsBelowIt(t *testing.T) {
	q := sq.New("ST1", 2)
	q.Join(entry("a", tr.PriorityNormal, 0, 0))
	q.Join(entry("b", tr.PriorityNormal, time.Minute, 0))
	q.Admit(t0)
	q.SetBufferLimit(1)
	q.Join(entry("c", tr.PriorityNormal, 0, 0))
	if len(q.Admit(t0)) != 0 {
		t.Fatal("admitted above the lowered buffer")
	}
	q.Leave("C-a")
	if len(q.Admit(t0)) != 0 {
		t.Fatal("occupied == limit admits nothing")
	}
	q.Leave("C-b")
	if got := q.Admit(t0); !same(ids(got), []string{"c"}) || q.BufferLimit() != 1 {
		t.Fatalf("admitted = %v", ids(got))
	}
}

func TestRestoreRoundTrips(t *testing.T) {
	q := sq.New("ST1", 2)
	q.Join(entry("a", tr.PriorityUrgent, 0, 0))
	q.Join(entry("b", tr.PriorityNormal, 0, 0))
	q.Admit(t0)
	s := q.Snapshot()
	r := sq.Restore(s)
	if r.StationID() != "ST1" || r.Occupied() != 2 || s.NextSeq != 3 || r.Position("C-b") != 2 {
		t.Fatalf("restored = %+v", r.Snapshot())
	}
	s.Occupants[0].OrderID = "x"
	if r.Occupants()[0].OrderID != "a" {
		t.Fatal("the snapshot shares its slices")
	}
}
