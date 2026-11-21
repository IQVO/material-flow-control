package mcp_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	inboundmcp "github.com/IQVO/material-flow-control/internal/adapters/inbound/mcp"
	"github.com/IQVO/material-flow-control/internal/adapters/outbound/memory"
	"github.com/IQVO/material-flow-control/internal/application/usecases"
	"github.com/IQVO/material-flow-control/internal/config"
)

var t0 = time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)

// harness is a connected in-memory MCP client over the real server, wired to a usecases.Reader over
// in-memory repositories the way cmd/mcp wires it over Postgres. The store is written ONLY here, to
// seed state the way cmd/api would: the MCP surface has no write path.
type harness struct {
	t       *testing.T
	session *sdk.ClientSession
	store   *memory.Store
}

func newReader(t *testing.T) (*usecases.Reader, *memory.Store) {
	t.Helper()
	l, err := config.LoadLayout("../../../../config/flow-layout.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	st := memory.NewStore()
	r, err := usecases.NewReader(l, usecases.DefaultConfig(), usecases.ReadDeps{Requests: memory.NewRequests(st),
		Orders: memory.NewOrders(st), Tracks: memory.NewTracks(st), Stations: memory.NewStations(st),
		Capacities: memory.NewCapacities(st), Units: memory.NewUnits(st), Clock: memory.NewClock(t0)})
	if err != nil {
		t.Fatal(err)
	}
	return r, st
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	r, st := newReader(t)
	return &harness{t: t, session: connect(t, inboundmcp.Deps{Reads: r}), store: st}
}

func connect(t *testing.T, deps inboundmcp.Deps) *sdk.ClientSession {
	t.Helper()
	ctx := context.Background()
	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	serverSession, err := inboundmcp.NewServer(deps).Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	session, err := sdk.NewClient(&sdk.Implementation{Name: "test-client", Version: "0"}, nil).Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func (h *harness) call(name, args string) *sdk.CallToolResult {
	h.t.Helper()
	res, err := h.session.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: json.RawMessage(args)})
	if err != nil {
		h.t.Fatalf("call %s: %v", name, err)
	}
	return res
}

// structured decodes a successful result's structured content into out.
func (h *harness) structured(res *sdk.CallToolResult, out any) {
	h.t.Helper()
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		h.t.Fatalf("tool errored: %s", raw)
	}
	raw, _ := json.Marshal(res.StructuredContent)
	if err := json.Unmarshal(raw, out); err != nil {
		h.t.Fatalf("decode %s: %v", raw, err)
	}
}

// errorText is the text of an isError result ("" when the call succeeded).
func errorText(res *sdk.CallToolResult) string {
	if !res.IsError {
		return ""
	}
	raw, _ := json.Marshal(res.Content)
	return string(raw)
}
