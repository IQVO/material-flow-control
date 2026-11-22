package containertrack_test

import (
	"testing"

	ct "github.com/IQVO/material-flow-control/internal/domain/containertrack"
)

// ruling P4-R8: Version is the repository's optimistic counter; Restore keeps it.
func TestRestoreKeepsTheTrackVersion(t *testing.T) {
	tk := ct.New("TOTE-1", t0)
	if tk.Version() != 0 {
		t.Fatalf("a new track's version = %d", tk.Version())
	}
	s := tk.Snapshot()
	s.Version = 3
	if r := ct.Restore(s); r.Version() != 3 {
		t.Fatalf("restored version = %d, want 3", r.Version())
	}
}
