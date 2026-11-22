// Package mcp is material-flow-control's inbound Model Context Protocol adapter: a second driving adapter
// over the SAME read queries the REST adapter serves, through the usecases.Reader (ADR 0007), built on
// the official MCP Go SDK and served over Streamable HTTP only, stateless.
//
// The surface is READ-ONLY: every tool reads this context's own state (transport requests and their
// legs, container tracks, station queues, subsystem capacity, AS/RS unit states). Requesting,
// cancelling and resolving a clarification stay on REST, where the Idempotency-Key, the flow lock and
// the outbox live; governance_test.go fails the build on a write-verb tool name. The package depends
// on the application and domain layers only (never an outbound adapter, the REST adapter or cmd), and
// nothing but cmd/mcp imports it (TestMCPAdapterDependencyRule). There is no auth of any kind (fleet
// decision 2026-09-11): the in-cluster ClusterIP boundary is the access control. Equipment ids (robot
// keys, storage job and leg ids) appear here and in the A2A view only, never on relationship 1.
package mcp

import (
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// serverName and serverVersion identify this server in the MCP handshake.
const (
	serverName    = "material-flow-control-mcp"
	serverVersion = "1.0.0"
)

// instructions is what an MCP client is told about this server.
const instructions = "material-flow-control (read-only): the WCS material flow controller — transport requests and their " +
	"legs, container positions, goods-to-person station queues and subsystem capacity. Every tool is read-only (authority " +
	"L1); nothing here moves a container."

// NewServer builds the MCP server with every tool, resource and prompt registered.
func NewServer(deps Deps) *mcp.Server {
	server := mcp.NewServer(
		&mcp.Implementation{Name: serverName, Version: serverVersion},
		&mcp.ServerOptions{Instructions: instructions},
	)
	deps.registerTools(server)
	deps.registerResources(server)
	deps.registerPrompts(server)
	return server
}

// Handler returns the Streamable HTTP handler. Stateless (Agentic Standard v2, AS-4): no
// Mcp-Session-Id is issued, so the 2026-07-28 revision's per-request handshake works and legacy
// initialize clients keep working; nothing relies on cross-call session state.
func Handler(server *mcp.Server) http.Handler {
	return mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{Stateless: true},
	)
}
