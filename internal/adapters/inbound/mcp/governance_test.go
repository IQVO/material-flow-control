package mcp_test

import (
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// update regenerates testdata/tool_registry.golden.json:
//
//	go test ./internal/adapters/inbound/mcp -run TestToolRegistryGolden -update
var update = flag.Bool("update", false, "rewrite the MCP tool registry golden file")

// wantTools is the curated, READ-ONLY tool set (ADR 0007), alphabetical like the SDK lists them.
var wantTools = []string{"get_container_track", "get_station", "get_transport", "list_equipment_units", "list_subsystems"}

func listTools(t *testing.T, h *harness) []*sdk.Tool {
	t.Helper()
	listed, err := h.session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	return listed.Tools
}

func TestToolSurface(t *testing.T) {
	var names []string
	for _, tool := range listTools(t, newHarness(t)) {
		names = append(names, tool.Name)
	}
	if strings.Join(names, ",") != strings.Join(wantTools, ",") {
		t.Fatalf("advertised tools %v, want exactly %v", names, wantTools)
	}
}

// writeVerbViolations is the write-verb sensor: no tool name may start with a write verb of this
// context (request, cancel, resolve, clarify, dispatch, move, store, retrieve, confirm) or a generic mutator.
func writeVerbViolations(names []string) []string {
	verbs := []string{"request", "cancel", "resolve", "clarify", "dispatch", "move", "store", "retrieve", "confirm",
		"create", "delete", "update", "set", "post", "send"}
	var out []string
	for _, n := range names {
		for _, v := range verbs {
			if strings.HasPrefix(n, v+"_") || n == v {
				out = append(out, n)
				break
			}
		}
	}
	return out
}

func TestWriteVerbSensorFailsOnWriteNames(t *testing.T) {
	if got := writeVerbViolations(wantTools); len(got) != 0 {
		t.Fatalf("write verbs on the read-only surface: %v", got)
	}
	if got := writeVerbViolations([]string{"get_x", "cancel_transport", "resolve_clarification"}); len(got) != 2 {
		t.Fatalf("sensor missed a write verb: %v", got)
	}
}

func TestEveryToolIsReadOnlyClosedWorldAndL1(t *testing.T) {
	for _, tool := range listTools(t, newHarness(t)) {
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint || !tool.Annotations.IdempotentHint {
			t.Errorf("tool %s is not annotated read-only and idempotent", tool.Name)
			continue
		}
		if tool.Annotations.OpenWorldHint == nil || *tool.Annotations.OpenWorldHint {
			t.Errorf("tool %s is not closed-world", tool.Name)
		}
		if level, _ := tool.Meta["com.warehouse/authority"].(string); level != "L1" {
			t.Errorf("tool %s authority = %v, want L1", tool.Name, tool.Meta)
		}
	}
}

// TestToolRegistryGolden pins the whole advertised registry (names, descriptions, annotations,
// input and output schemas) byte for byte, so any change an MCP client would see is a reviewed diff.
func TestToolRegistryGolden(t *testing.T) {
	tools := listTools(t, newHarness(t))
	sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
	got, err := json.MarshalIndent(tools, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	path := filepath.Join("testdata", "tool_registry.golden.json")
	if *update {
		if err := os.WriteFile(path, got, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (run with -update to create it): %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("the MCP tool registry drifted from %s; review the change and re-run with -update.\ngot:\n%s", path, got)
	}
}

func TestOptionalInputsAreNotRequired(t *testing.T) {
	for _, tool := range listTools(t, newHarness(t)) {
		raw, _ := json.Marshal(tool.InputSchema)
		var schema struct {
			Required []string `json:"required"`
		}
		_ = json.Unmarshal(raw, &schema)
		for _, req := range schema.Required {
			if req == "cursor" || req == "limit" || req == "status" {
				t.Errorf("tool %s requires optional input %s", tool.Name, req)
			}
		}
	}
}
