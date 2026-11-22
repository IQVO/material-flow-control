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

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"

	inbounda2a "github.com/IQVO/material-flow-control/internal/adapters/inbound/a2a"
)

const testAgentURL = "http://material-flow-control-a2a.warehouse-systems.svc.cluster.local:8093/"

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

// newTestServer serves the real router over the real handler; an unknown skill is answered before any read
// runs, so the zero Deps is enough for the transport tests.
func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(newAgent(inbounda2a.Deps{}, testAgentURL, "test", quiet(), "material-flow-control-a2a"))
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

// The card is served at the well-known path, advertises the configured URL (not the listener's) and needs no
// credentials.
func TestTheAgentCardIsServedAtTheWellKnownPathWithoutAuth(t *testing.T) {
	srv := newTestServer(t)
	for _, auth := range []string{"", "Bearer nonsense"} {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/.well-known/agent-card.json", nil)
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var card a2a.AgentCard
		err = json.NewDecoder(resp.Body).Decode(&card)
		_ = resp.Body.Close()
		if err != nil || resp.StatusCode != http.StatusOK || card.Name != "material-flow-control" || len(card.Skills) != 5 ||
			card.SupportedInterfaces[0].URL != testAgentURL || card.SecuritySchemes != nil || card.Version != "test" {
			t.Fatalf("card (Authorization=%q) = %d %+v (%v)", auth, resp.StatusCode, card, err)
		}
	}
}

// Both mount points speak JSON-RPC: a real SDK client built from the card reaches the executor.
func TestRootAndA2APathsAnswerSkills(t *testing.T) {
	srv := newTestServer(t)
	for _, path := range []string{"/", "/a2a"} {
		client, err := a2aclient.NewFromCard(context.Background(), inbounda2a.Card(inbounda2a.Config{AgentURL: srv.URL + path, Version: "t"}))
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		part := a2a.NewDataPart(map[string]any{"skill": "no_such_skill", "arguments": map[string]any{}})
		resp, err := client.SendMessage(context.Background(), &a2a.SendMessageRequest{Message: a2a.NewMessage(a2a.MessageRoleUser, part)})
		if err != nil {
			t.Fatalf("%s: SendMessage: %v", path, err)
		}
		msg, ok := resp.(*a2a.Message)
		if !ok || len(msg.Parts) != 2 {
			t.Fatalf("%s: reply = %T", path, resp)
		}
		raw, _ := json.Marshal(msg.Parts[0].Content.(a2a.Data).Value)
		if !bytes.Contains(raw, []byte(`"code":"unknown-skill"`)) {
			t.Fatalf("%s: answer = %s, want the in-band unknown-skill problem", path, raw)
		}
	}
}

func TestEveryRequiredVariableIsNamedWhenMissing(t *testing.T) {
	_, err := loadConfig(env(map[string]string{}))
	for _, v := range []string{"A2A_AGENT_URL", "DATABASE_URL", "MFC_LAYOUT_FILE"} {
		if err == nil || !strings.Contains(err.Error(), v) {
			t.Errorf("the boot error does not name %s: %v", v, err)
		}
	}
	all := map[string]string{"A2A_AGENT_URL": testAgentURL, "DATABASE_URL": "postgres://db", "MFC_LAYOUT_FILE": "/l.yaml"}
	for missing := range all {
		set := map[string]string{}
		for k, v := range all {
			if k != missing {
				set[k] = v
			}
		}
		_, err := loadConfig(env(set))
		if err == nil || !strings.Contains(err.Error(), missing) || strings.Count(err.Error(), "is required") != 1 {
			t.Errorf("missing only %s: %v", missing, err)
		}
	}
}

func TestTheAgentURLMustBeAnAbsoluteHTTPURL(t *testing.T) {
	base := map[string]string{"DATABASE_URL": "postgres://db", "MFC_LAYOUT_FILE": "/l.yaml"}
	for _, bad := range []string{"material-flow-control-a2a:8093", "/relative", "ftp://host/", "http://", "  ", "http://%zz"} {
		base["A2A_AGENT_URL"] = bad
		if _, err := loadConfig(env(base)); err == nil || !strings.Contains(err.Error(), "A2A_AGENT_URL") {
			t.Errorf("A2A_AGENT_URL=%q booted: %v", bad, err)
		}
	}
	base["A2A_AGENT_URL"] = "https://agent.example/a2a"
	if _, err := loadConfig(env(base)); err != nil {
		t.Fatalf("an https agent URL: %v", err)
	}
}

func TestConfigDefaults(t *testing.T) {
	c, err := loadConfig(env(map[string]string{"A2A_AGENT_URL": " " + testAgentURL + " ", "DATABASE_URL": "postgres://db", "MFC_LAYOUT_FILE": "/l.yaml"}))
	if err != nil || c.Addr != ":8093" || c.ServiceName != "material-flow-control-a2a" || c.AgentURL != testAgentURL ||
		c.MigrationsURL != "postgres://db" || c.OTLPEndpoint != "" || c.LogLevel != "info" || c.LayoutFile != "/l.yaml" ||
		c.DatabaseURL != "postgres://db" || c.ServiceVersion != "" {
		t.Fatalf("defaults = %+v (%v)", c, err)
	}
}

func TestConfigOverrides(t *testing.T) {
	c, _ := loadConfig(env(map[string]string{"A2A_AGENT_URL": testAgentURL, "DATABASE_URL": "postgres://db", "MFC_LAYOUT_FILE": "/l.yaml",
		"A2A_ADDR": ":9", "MIGRATIONS_DATABASE_URL": "postgres://direct", "OTEL_SERVICE_NAME": "x", "SERVICE_VERSION": "v1",
		"OTEL_EXPORTER_OTLP_ENDPOINT": "c:4317", "LOG_LEVEL": "debug"}))
	if c.Addr != ":9" || c.MigrationsURL != "postgres://direct" || c.ServiceName != "x" || c.ServiceVersion != "v1" ||
		c.OTLPEndpoint != "c:4317" || c.LogLevel != "debug" {
		t.Fatalf("overrides = %+v", c)
	}
}

func TestCardVersionFallsBackToDev(t *testing.T) {
	if cardVersion("") != "dev" || cardVersion("v1.2.3") != "v1.2.3" {
		t.Fatal("cardVersion")
	}
}

// ruling P4-R13 (cmd/api and cmd/mcp parity): a boot error is ONE JSON line naming every missing variable, exit 1.
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
	if rec["level"] != "ERROR" || rec["msg"] != "material-flow-control a2a exited with error" || !strings.Contains(msg, "A2A_AGENT_URL") ||
		!strings.Contains(msg, "DATABASE_URL") || !strings.Contains(msg, "MFC_LAYOUT_FILE") {
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
	flush, err := startTelemetry(newLogger(&buf, "info"), "material-flow-control-a2a", "", "")
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
