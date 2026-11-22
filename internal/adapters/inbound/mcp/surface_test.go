package mcp

import (
	"context"
	"sort"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func session(t *testing.T, d Deps) *mcp.ClientSession {
	t.Helper()
	server := NewServer(d)
	st, ct := mcp.NewInMemoryTransports()
	if _, err := server.Connect(context.Background(), st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// TestToolSurfaceIsExactlyTheFiveReadOnlyL1Tools is task-20-brief Step 2's test split into helpers for cyclop
// (max 15): every assertion line is byte-identical to the brief's and runs in the same order.
func TestToolSurfaceIsExactlyTheFiveReadOnlyL1Tools(t *testing.T) {
	cs := session(t, Deps{Reads: &fakeReads{}})
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	names := readOnlyL1Names(t, res)
	sort.Strings(names)
	want := []string{"get_container_track", "get_station", "get_transport", "list_equipment_units", "list_subsystems"}
	assertToolNames(t, names, want)
	assertThePromptAndTheTemplate(t, cs)
}

func readOnlyL1Names(t *testing.T, res *mcp.ListToolsResult) []string {
	t.Helper()
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
		a := tool.Annotations
		if a == nil || !a.ReadOnlyHint || !a.IdempotentHint || a.OpenWorldHint == nil || *a.OpenWorldHint || tool.Meta[authorityMetaKey] != "L1" {
			t.Fatalf("%s: annotations %+v meta %v", tool.Name, a, tool.Meta)
		}
	}
	return names
}

func assertToolNames(t *testing.T, names, want []string) {
	t.Helper()
	if len(names) != len(want) {
		t.Fatalf("tools = %v", names)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("tools = %v, want %v", names, want)
		}
	}
}

func assertThePromptAndTheTemplate(t *testing.T, cs *mcp.ClientSession) {
	t.Helper()
	if p, err := cs.ListPrompts(context.Background(), nil); err != nil || len(p.Prompts) != 1 || p.Prompts[0].Name != "review_transport" {
		t.Fatalf("prompts = %+v %v", p, err)
	}
	if r, err := cs.ListResourceTemplates(context.Background(), nil); err != nil || len(r.ResourceTemplates) != 1 ||
		r.ResourceTemplates[0].URITemplate != "warehouse://material-flow-control/stations/{stationId}" {
		t.Fatalf("templates = %+v %v", r, err)
	}
}
