package stationqueue_test

import (
	"testing"
	"time"

	sq "github.com/IQVO/material-flow-control/internal/domain/stationqueue"
	tr "github.com/IQVO/material-flow-control/internal/domain/transportrequest"
)

// carry (Task 5 review, Task 26 mutation work): Admit on an empty queue with free slots admits nothing (kills the
// len(w) > 0 -> >= 0 loop mutant, which indexes an empty slice); one waiting entry and two free slots admit exactly it.
func TestAdmitOnAnEmptyQueueAdmitsNothingThenExactlyTheOneWaiting(t *testing.T) {
	q := sq.New("ST1", 2)
	if got := q.Admit(t0); len(got) != 0 || q.Occupied() != 0 {
		t.Fatalf("Admit on an empty queue = %+v, occupied %d; want nothing", got, q.Occupied())
	}
	q.Join(entry("a", tr.PriorityNormal, 0, 0))
	if got := q.Admit(t0.Add(time.Minute)); !same(ids(got), []string{"a"}) || q.Occupied() != 1 || len(q.Waiting()) != 0 {
		t.Fatalf("Admit with one waiting and two free = %+v, occupied %d, waiting %d", got, q.Occupied(), len(q.Waiting()))
	}
}

// carry (Task 5 review): Snapshot and Restore copy the waiting entries; changing one copy leaves the queue alone.
func TestSnapshotAndRestoreDoNotShareTheWaitingEntries(t *testing.T) {
	q := sq.New("ST1", 1)
	q.Join(entry("a", tr.PriorityNormal, 0, 0))
	s := q.Snapshot()
	s.Waiting[0].OrderID = "changed"
	if w := q.Waiting(); w[0].OrderID != "a" {
		t.Fatalf("a changed snapshot reached the queue: %+v", w)
	}
	r := sq.Restore(s)
	s.Waiting[0].OrderID = "again"
	if w := r.Waiting(); w[0].OrderID != "changed" {
		t.Fatalf("a changed snapshot reached the restored queue: %+v", w)
	}
}
