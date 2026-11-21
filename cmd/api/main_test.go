package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

// ruling P4-R13: the boot error reaches the fleet log pipeline as ONE JSON line naming every invalid or
// missing variable, and the process exits 1.
func TestABootErrorIsOneJSONLineNamingEveryBadVariable(t *testing.T) {
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })
	var out bytes.Buffer
	if code := boot(&out, env(map[string]string{"TICK_INTERVAL_MS": "0", "LEG_TIMEOUT_SECONDS": "soon"})); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("boot wrote %d lines, want one JSON line: %q", len(lines), out.String())
	}
	var rec map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &rec); err != nil {
		t.Fatalf("boot error is not JSON: %v: %q", err, lines[0])
	}
	msg, _ := rec["error"].(string)
	if rec["level"] != "ERROR" || msg == "" {
		t.Fatalf("record = %v, want level ERROR with an error field", rec)
	}
	for _, name := range []string{"DATABASE_URL", "KAFKA_BROKERS", "ASRS_CONTROL_URL", "AMR_FLEET_CONTROL_URL", "MFC_LAYOUT_FILE",
		"TICK_INTERVAL_MS", "LEG_TIMEOUT_SECONDS"} {
		if !strings.Contains(msg, name) {
			t.Errorf("boot error %q does not name %s", msg, name)
		}
	}
}
