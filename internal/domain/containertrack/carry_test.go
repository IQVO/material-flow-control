package containertrack_test

import (
	"testing"
	"time"

	ct "github.com/IQVO/material-flow-control/internal/domain/containertrack"
)

// ruling P4-R5: one container is one exception case (responsibility 4, D12). A second SendToClarification on a
// track already held keeps the first hold (reason and Since unchanged) and raises no event (the zero value).
func TestASecondClarificationKeepsTheFirstHoldAndRaisesNoEvent(t *testing.T) {
	tk := ct.New("TOTE-1", t0)
	first := tk.SendToClarification(ct.Clarification{Reason: ct.ReasonNoRead, DetectedAt: "A-OUT1"}, t0)
	if first.Reason != ct.ReasonNoRead || first.ContainerID != "TOTE-1" || !first.At.Equal(t0) {
		t.Fatalf("first = %+v", first)
	}
	again := tk.SendToClarification(ct.Clarification{Reason: ct.ReasonMismatch, ScannedContainerID: "TOTE-9", DetectedAt: "A-IN1"}, t0.Add(time.Hour))
	if again != (ct.ContainerSentToClarification{}) {
		t.Fatalf("a second clarification raised %+v", again)
	}
	hold, ok := tk.Clarification()
	if !ok || hold.Reason != ct.ReasonNoRead || !hold.Since.Equal(t0) || hold.ScannedContainerID != "" || hold.DetectedAt != "A-OUT1" {
		t.Fatalf("hold = %+v %v, want the first (NO_READ since t0)", hold, ok)
	}
	if !tk.Snapshot().UpdatedAt.Equal(t0) {
		t.Fatalf("a no-op moved UpdatedAt to %v", tk.Snapshot().UpdatedAt)
	}
}
