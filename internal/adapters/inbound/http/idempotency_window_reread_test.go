package http_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	ct "github.com/IQVO/material-flow-control/internal/domain/containertrack"
)

// carry (Task 19 review): the unstored-refusal re-read honours the 24 h window (the second review's
// probe). A key whose answer is older than 24 h is released: a later RETRYABLE refusal under it is the
// request's own answer, never the expired replay.
func TestTheUnstoredRefusalReReadHonoursTheWindow(t *testing.T) {
	f, _, _ := loggedFixture(t)
	tr := ct.New("TOTE-W", t0)
	tr.MoveTo(ct.Position{Kind: ct.InAsrs, Code: asrsLoc}, t0)
	tr.SendToClarification(ct.Clarification{Reason: ct.ReasonNoRead, DetectedAt: asrsLoc}, t0)
	if err := f.tracks.Save(context.Background(), tr); err != nil {
		t.Fatal(err)
	}
	path := "/container-tracks/TOTE-W/clarification-resolution"
	if first, b := f.do(path, "k-old", nil); first.Code != http.StatusOK {
		t.Fatalf("first = %d %v", first.Code, b)
	}
	f.clock.Advance(25 * time.Hour)
	again, body := f.do(path, "k-old", nil)
	if again.Code != http.StatusConflict || replayed(again) || body["code"] != "not-in-clarification" {
		t.Fatalf("after 25 h = %d replayed=%v %v, want its own 409 not-in-clarification", again.Code, replayed(again), body)
	}
}
