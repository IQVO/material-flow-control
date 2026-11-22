package containertrack_test

import (
	"errors"
	"testing"
	"time"

	ct "github.com/IQVO/material-flow-control/internal/domain/containertrack"
)

// carry (Task 4 review): a held container that also has an active order answers ErrInClarification first; Reserve
// and Release set the request id and UpdatedAt; Restore keeps Version, ActiveRequestID and UpdatedAt, and a Snapshot
// does not share the clarification with the track.
func TestReserveReleaseRestoreAndTheHeldBeforeBusyPrecedence(t *testing.T) {
	t1, t2 := t0.Add(time.Minute), t0.Add(2*time.Minute)
	tr := ct.New("TOTE-1", t0)
	if err := tr.Reserve("to-1", "rq-1", t1); err != nil || tr.ActiveRequestID() != "rq-1" || !tr.Snapshot().UpdatedAt.Equal(t1) {
		t.Fatalf("Reserve = %v, request %q, updated %s", err, tr.ActiveRequestID(), tr.Snapshot().UpdatedAt)
	}
	tr.SendToClarification(ct.Clarification{Reason: ct.ReasonNoRead}, t1)
	if err := tr.Dispatchable(); !errors.Is(err, ct.ErrInClarification) {
		t.Fatalf("held and busy: Dispatchable = %v, want ErrInClarification first", err)
	}
	tr.Release("to-1", t2)
	if tr.ActiveOrderID() != "" || tr.ActiveRequestID() != "" || !tr.Snapshot().UpdatedAt.Equal(t2) {
		t.Fatalf("after Release: order %q request %q updated %s", tr.ActiveOrderID(), tr.ActiveRequestID(), tr.Snapshot().UpdatedAt)
	}
	s := tr.Snapshot()
	s.Version, s.ActiveRequestID = 7, "rq-9"
	r := ct.Restore(s)
	if r.Version() != 7 || r.ActiveRequestID() != "rq-9" || !r.Snapshot().UpdatedAt.Equal(t2) {
		t.Fatalf("restored = version %d request %q updated %s", r.Version(), r.ActiveRequestID(), r.Snapshot().UpdatedAt)
	}
	snap := r.Snapshot()
	snap.Clarification.Reason = ct.ReasonMismatch
	if c, _ := r.Clarification(); c.Reason != ct.ReasonNoRead {
		t.Fatalf("a changed snapshot reached the track's clarification: %+v", c)
	}
}
