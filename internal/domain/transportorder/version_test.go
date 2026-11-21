package transportorder_test

import (
	"testing"

	to "github.com/IQVO/material-flow-control/internal/domain/transportorder"
	tr "github.com/IQVO/material-flow-control/internal/domain/transportrequest"
)

// ruling P4-R8: Version is the repository's optimistic counter; Restore keeps it and the source.
func TestRestoreKeepsTheOrderVersionAndSource(t *testing.T) {
	o := accept(t, tr.PurposePick, asrs, station, pickPlan())
	if o.Version() != 0 || o.Source() != asrs {
		t.Fatalf("a new order: version %d, source %+v", o.Version(), o.Source())
	}
	s := o.Snapshot()
	s.Version = 5
	if r := to.Restore(s); r.Version() != 5 || r.Source() != asrs {
		t.Fatalf("restored version = %d, source %+v", r.Version(), r.Source())
	}
}
