package stationqueue_test

import (
	"testing"

	sq "github.com/IQVO/material-flow-control/internal/domain/stationqueue"
)

// ruling P4-R8: Version is the repository's optimistic counter; Restore keeps it.
func TestRestoreKeepsTheQueueVersion(t *testing.T) {
	q := sq.New("ST1", 2)
	if q.Version() != 0 {
		t.Fatalf("a new queue's version = %d", q.Version())
	}
	s := q.Snapshot()
	s.Version = 4
	if r := sq.Restore(s); r.Version() != 4 {
		t.Fatalf("restored version = %d, want 4", r.Version())
	}
}
