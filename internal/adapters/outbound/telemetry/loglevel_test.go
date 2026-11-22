package telemetry

import (
	"log/slog"
	"strings"
	"testing"
)

// carry (Task 22 review): ParseLogLevel is the one LOG_LEVEL vocabulary of the three binaries.
func TestParseLogLevel(t *testing.T) {
	for _, c := range []struct {
		in   string
		want slog.Level
	}{{"debug", slog.LevelDebug}, {"info", slog.LevelInfo}, {"warn", slog.LevelWarn}, {"warning", slog.LevelWarn},
		{"error", slog.LevelError}, {" ERROR ", slog.LevelError}, {"Info", slog.LevelInfo}} {
		if got, err := ParseLogLevel(c.in); err != nil || got != c.want {
			t.Errorf("ParseLogLevel(%q) = %v, %v; want %v", c.in, got, err, c.want)
		}
	}
	for _, bad := range []string{"", "verbose", "trace", "0"} {
		got, err := ParseLogLevel(bad)
		if err == nil || !strings.Contains(err.Error(), "LOG_LEVEL") || got != slog.LevelInfo {
			t.Errorf("ParseLogLevel(%q) = %v, %v; want info and an error naming LOG_LEVEL", bad, got, err)
		}
	}
}

// carry (Task 22 review): the boot error lists every accepted value, the "warning" alias included.
func TestParseLogLevelErrorListsTheWarningAlias(t *testing.T) {
	_, err := ParseLogLevel("verbose")
	if err == nil || !strings.Contains(err.Error(), "warning") {
		t.Fatalf("ParseLogLevel(verbose) error = %v; want it to list warning", err)
	}
}
