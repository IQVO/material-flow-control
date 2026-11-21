package mcp_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	inboundmcp "github.com/IQVO/material-flow-control/internal/adapters/inbound/mcp"
)

// Stateless (Agentic Standard v2, AS-4): no Mcp-Session-Id is issued, and a legacy initialize
// handshake and a sessionless tools/list both work over plain HTTP.

func newHTTPServer(t *testing.T) *httptest.Server {
	t.Helper()
	r, _ := newReader(t)
	srv := httptest.NewServer(inboundmcp.Handler(inboundmcp.NewServer(inboundmcp.Deps{Reads: r})))
	t.Cleanup(srv.Close)
	return srv
}

func post(t *testing.T, srv *httptest.Server, body string) (int, http.Header, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.URL, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, string(raw)
}

func TestInitializeIssuesNoSessionID(t *testing.T) {
	status, header, body := post(t, newHTTPServer(t), `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`)
	if status != http.StatusOK || header.Get("Mcp-Session-Id") != "" || !strings.Contains(body, "material-flow-control-mcp") {
		t.Fatalf("initialize = %d session %q body %s", status, header.Get("Mcp-Session-Id"), body)
	}
}

func TestToolsListOverHTTPWithoutASession(t *testing.T) {
	status, _, body := post(t, newHTTPServer(t), `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	if status != http.StatusOK || !strings.Contains(body, "get_transport") || !strings.Contains(body, "get_container_track") {
		t.Fatalf("tools/list = %d %s", status, body)
	}
}
