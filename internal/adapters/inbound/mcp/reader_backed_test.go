package mcp_test

import (
	"context"
	"strings"
	"testing"

	"github.com/IQVO/material-flow-control/internal/adapters/outbound/memory"
	ct "github.com/IQVO/material-flow-control/internal/domain/containertrack"
)

// The adapter over the real Reader (memory repositories): a stored track reads back with its position, an
// unknown container is the container-track-not-found tool error and a configured station answers its empty queue.
func TestTheToolsReadThroughTheRealReader(t *testing.T) {
	h := newHarness(t)
	track := ct.New("TOTE-1", t0)
	track.MoveTo(ct.Position{Kind: ct.InAsrs, Code: "WH1-AUTO-ASRS-ASRS1-00-00-S"}, t0)
	if err := memory.NewTracks(h.store).Save(context.Background(), track); err != nil {
		t.Fatal(err)
	}
	var got struct {
		ContainerID string `json:"container_id"`
		Position    struct{ Kind, Code string }
	}
	h.structured(h.call("get_container_track", `{"containerId":"TOTE-1"}`), &got)
	if got.ContainerID != "TOTE-1" || got.Position.Kind != "IN_ASRS" || got.Position.Code != "WH1-AUTO-ASRS-ASRS1-00-00-S" {
		t.Fatalf("get_container_track = %+v", got)
	}
	if text := errorText(h.call("get_container_track", `{"containerId":"TOTE-9"}`)); !strings.Contains(text, "container-track-not-found") {
		t.Fatalf("unknown container = %q", text)
	}
	var st struct {
		StationID   string `json:"station_id"`
		BufferLimit int    `json:"buffer_limit"`
	}
	h.structured(h.call("get_station", `{"stationId":"ST1"}`), &st)
	if st.StationID != "ST1" || st.BufferLimit != 2 {
		t.Fatalf("get_station = %+v", st)
	}
	if text := errorText(h.call("get_transport", `{"transportRequestId":"rq-9"}`)); !strings.Contains(text, "transport-request-not-found") {
		t.Fatalf("unknown transport = %q", text)
	}
}
