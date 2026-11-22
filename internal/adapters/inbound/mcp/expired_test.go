package mcp

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/IQVO/material-flow-control/internal/application/usecases"
)

// expiredReads answers every transport read as one whose order the retention purged (ruling P4-R22).
type expiredReads struct{ fakeReads }

func (expiredReads) Transport(_ context.Context, id string) (usecases.TransportView, error) {
	return usecases.TransportView{}, fmt.Errorf("read %s: %w", id, usecases.ErrTransportExpired)
}

// ruling P4-R22: get_transport of an id whose order was purged is the REST slug transport-request-expired, never an
// internal-error, and the tool's description names it.
func TestAnExpiredTransportIsTransportRequestExpired(t *testing.T) {
	cs := session(t, Deps{Reads: expiredReads{}})
	res, err := cs.CallTool(context.Background(),
		&mcp.CallToolParams{Name: "get_transport", Arguments: map[string]any{"transportRequestId": "task-1.1"}})
	if err != nil {
		t.Fatal(err)
	}
	if text := errorContent(t, res); !res.IsError || !strings.Contains(text, "transport-request-expired: read task-1.1: the transport request has expired") {
		t.Fatalf("expired transport = %v %s", res.IsError, text)
	}
	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools.Tools {
		if tool.Name == "get_transport" && !strings.Contains(tool.Description, "transport-request-expired") {
			t.Fatalf("get_transport description %q does not name transport-request-expired", tool.Description)
		}
	}
}
