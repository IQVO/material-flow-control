package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	inboundmcp "github.com/IQVO/material-flow-control/internal/adapters/inbound/mcp"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

// newTestServer serves the real MCP server behind the real router; listing tools needs no reads.
func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(newRouter(inboundmcp.Handler(inboundmcp.NewServer(inboundmcp.Deps{})), quiet(), "material-flow-control-mcp"))
	t.Cleanup(srv.Close)
	return srv
}

func TestHealthzIsOpen(t *testing.T) {
	resp, err := http.Get(newTestServer(t).URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || string(body) != `{"status":"ok"}` || resp.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("GET /healthz = %d %q", resp.StatusCode, body)
	}
}

// Both mount points speak Streamable HTTP without credentials, and every tool is read-only.
func TestRootAndMCPPathsServeTheReadOnlyTools(t *testing.T) {
	srv := newTestServer(t)
	for _, path := range []string{"/", "/mcp"} {
		session, err := sdk.NewClient(&sdk.Implementation{Name: "t", Version: "0"}, nil).
			Connect(context.Background(), &sdk.StreamableClientTransport{Endpoint: srv.URL + path}, nil)
		if err != nil {
			t.Fatalf("connect %s: %v", path, err)
		}
		tools, err := session.ListTools(context.Background(), nil)
		if err != nil || len(tools.Tools) != 5 {
			t.Fatalf("%s: tools = %v (%v), want 5", path, tools, err)
		}
		for _, tool := range tools.Tools {
			if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
				t.Errorf("%s: tool %s is not read-only", path, tool.Name)
			}
		}
		_ = session.Close()
	}
}

// No key is read: a request with or without an Authorization header behaves the same.
func TestNoAuthIsRequired(t *testing.T) {
	srv := newTestServer(t)
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`
	for _, auth := range []string{"", "Bearer nonsense"} {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/mcp", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("initialize with Authorization=%q = %d", auth, resp.StatusCode)
		}
	}
}

func TestAMissingDatabaseURLOrLayoutFileIsAnErrorNamingIt(t *testing.T) {
	_, err := loadConfig(env(map[string]string{}))
	for _, v := range []string{"DATABASE_URL", "MFC_LAYOUT_FILE"} {
		if err == nil || !strings.Contains(err.Error(), v) {
			t.Errorf("the boot error does not name %s: %v", v, err)
		}
	}
	for missing, set := range map[string]map[string]string{
		"DATABASE_URL":    {"MFC_LAYOUT_FILE": "/l.yaml"},
		"MFC_LAYOUT_FILE": {"DATABASE_URL": "postgres://db"},
	} {
		_, err := loadConfig(env(set))
		if err == nil || !strings.Contains(err.Error(), missing) || strings.Count(err.Error(), "is required") != 1 {
			t.Errorf("missing only %s: %v", missing, err)
		}
	}
}

func TestConfigDefaultsAndOverrides(t *testing.T) {
	c, err := loadConfig(env(map[string]string{"DATABASE_URL": "postgres://db", "MFC_LAYOUT_FILE": "/l.yaml"}))
	if err != nil || c.Addr != ":8090" || c.MigrationsURL != "postgres://db" || c.ServiceName != "material-flow-control-mcp" ||
		c.OTLPEndpoint != "" || c.LogLevel != "info" || c.LayoutFile != "/l.yaml" || c.DatabaseURL != "postgres://db" {
		t.Fatalf("defaults = %+v (%v)", c, err)
	}
	c, _ = loadConfig(env(map[string]string{"DATABASE_URL": "postgres://db", "MFC_LAYOUT_FILE": "/l.yaml", "MCP_ADDR": ":9",
		"MIGRATIONS_DATABASE_URL": "postgres://direct", "OTEL_SERVICE_NAME": "x", "SERVICE_VERSION": "v1",
		"OTEL_EXPORTER_OTLP_ENDPOINT": "c:4317", "LOG_LEVEL": "debug"}))
	if c.Addr != ":9" || c.MigrationsURL != "postgres://direct" || c.ServiceName != "x" || c.ServiceVersion != "v1" ||
		c.OTLPEndpoint != "c:4317" || c.LogLevel != "debug" {
		t.Fatalf("overrides = %+v", c)
	}
}

// ruling P4-R13 (cmd/api parity): a boot error is ONE JSON line naming every missing variable, exit code 1.
func TestABootErrorIsOneJSONLineAndExitCodeOne(t *testing.T) {
	var buf bytes.Buffer
	if code := boot(&buf, env(map[string]string{})); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	var rec map[string]any
	if len(lines) != 1 || json.Unmarshal([]byte(lines[0]), &rec) != nil {
		t.Fatalf("boot output = %q, want one JSON line", buf.String())
	}
	msg, _ := rec["error"].(string)
	if rec["level"] != "ERROR" || !strings.Contains(msg, "DATABASE_URL") || !strings.Contains(msg, "MFC_LAYOUT_FILE") {
		t.Fatalf("boot error record = %v", rec)
	}
}

func TestBuildReadsRefusesAMissingLayoutFile(t *testing.T) {
	if _, _, err := buildReads(context.Background(), config{LayoutFile: "/does/not/exist.yaml", DatabaseURL: "postgres://x"}); err == nil {
		t.Fatal("a missing layout file must fail boot")
	}
}

func TestStartTelemetryWithoutAnEndpointIsOff(t *testing.T) {
	var buf bytes.Buffer
	flush, err := startTelemetry(newLogger(&buf, "info"), "material-flow-control-mcp", "", "")
	if err != nil {
		t.Fatal(err)
	}
	flush()
	if !strings.Contains(buf.String(), "telemetry disabled") {
		t.Fatalf("boot log = %s", buf.String())
	}
}

func TestNewLoggerLevels(t *testing.T) {
	for level, want := range map[string]slog.Level{"debug": slog.LevelDebug, "warn": slog.LevelWarn, "warning": slog.LevelWarn, "error": slog.LevelError, "": slog.LevelInfo} {
		l := newLogger(io.Discard, level)
		if !l.Enabled(context.Background(), want) || (want > slog.LevelDebug && l.Enabled(context.Background(), want-1)) {
			t.Errorf("newLogger(%q): %v is not the minimum level", level, want)
		}
	}
}
