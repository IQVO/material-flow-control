package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// carry (Task 21 review): one argument rule on both agent surfaces (the A2A skills' `required`): an id is trimmed
// before it is read, and an id that is blank after trimming is invalid-argument, never a read.
func TestEveryToolTrimsItsIDAndRefusesABlankOne(t *testing.T) {
	if out, isErr := call(t, "get_transport", map[string]any{"transportRequestId": " task-1.1\t"}); isErr || out["transport_request_id"] != "task-1.1" {
		t.Fatalf("get_transport(' task-1.1') = %v %v, want the trimmed id read", out, isErr)
	}
	if out, isErr := call(t, "get_station", map[string]any{"stationId": " ST1 "}); isErr || out["station_id"] != "ST1" {
		t.Fatalf("get_station(' ST1 ') = %v %v, want the trimmed id read", out, isErr)
	}
	if out, isErr := call(t, "get_container_track", map[string]any{"containerId": " TOTE-1 "}); isErr || out["container_id"] != "TOTE-1" {
		t.Fatalf("get_container_track(' TOTE-1 ') = %v %v, want the trimmed id read", out, isErr)
	}
	if out, isErr := call(t, "list_equipment_units", map[string]any{"asrsId": " ASRS1 "}); isErr || out["asrs_id"] != "ASRS1" {
		t.Fatalf("list_equipment_units(' ASRS1 ') = %v %v, want the trimmed id read", out, isErr)
	}
	for tool, arg := range map[string]string{"get_transport": "transportRequestId", "get_container_track": "containerId",
		"get_station": "stationId", "list_equipment_units": "asrsId"} {
		res, err := session(t, Deps{Reads: fakeReads{}}).CallTool(context.Background(), &mcp.CallToolParams{Name: tool,
			Arguments: map[string]any{arg: "  "}})
		if err != nil {
			t.Fatal(err)
		}
		text := ""
		if len(res.Content) == 1 {
			if tc, ok := res.Content[0].(*mcp.TextContent); ok {
				text = tc.Text
			}
		}
		if !res.IsError || !strings.HasPrefix(text, "invalid-argument: "+arg+" is required") {
			t.Fatalf("%s(blank %s) = isError %v %q, want invalid-argument: %s is required", tool, arg, res.IsError, text, arg)
		}
	}
}

// carry (Task 21 review): each tool that reads one entity names the slug its unknown id fails with.
func TestEveryToolDescriptionNamesItsNotFoundSlug(t *testing.T) {
	tools, err := session(t, Deps{Reads: fakeReads{}}).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"get_transport": "transport-request-not-found", "get_container_track": "container-track-not-found",
		"get_station": "station-not-found", "list_equipment_units": "asrs-not-found"}
	for _, tool := range tools.Tools {
		if slug, ok := want[tool.Name]; ok && !strings.Contains(tool.Description, slug) {
			t.Fatalf("%s description %q does not name %s", tool.Name, tool.Description, slug)
		}
	}
}
