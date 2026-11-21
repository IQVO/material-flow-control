package main

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
)

// carry (Task 22 review; plan 03 Task 16 review parity): a LOG_LEVEL that is set but names no level is a boot error
// naming it, never a silent fallback to info; every documented level boots, in any case, and the logger honours it.
func TestASetButInvalidLogLevelIsABootError(t *testing.T) {
	for _, bad := range []string{"verbose", "trace", "inf0", "5"} {
		m := map[string]string{"DATABASE_URL": "postgres://db", "MFC_LAYOUT_FILE": "/l.yaml"}
		m["LOG_LEVEL"] = bad
		if _, err := loadConfig(env(m)); err == nil || !strings.Contains(err.Error(), "LOG_LEVEL") {
			t.Errorf("LOG_LEVEL=%q: err = %v, want a boot error naming LOG_LEVEL", bad, err)
		}
	}
	for good, want := range map[string]slog.Level{"debug": slog.LevelDebug, "info": slog.LevelInfo, "warn": slog.LevelWarn,
		"warning": slog.LevelWarn, "error": slog.LevelError, "DEBUG": slog.LevelDebug, "Warn": slog.LevelWarn} {
		m := map[string]string{"DATABASE_URL": "postgres://db", "MFC_LAYOUT_FILE": "/l.yaml"}
		m["LOG_LEVEL"] = good
		c, err := loadConfig(env(m))
		if err != nil {
			t.Errorf("LOG_LEVEL=%q: %v, want accepted", good, err)
			continue
		}
		l := newLogger(io.Discard, c.LogLevel)
		if !l.Enabled(context.Background(), want) || (want > slog.LevelDebug && l.Enabled(context.Background(), want-1)) {
			t.Errorf("LOG_LEVEL=%q: the logger's minimum level is not %v", good, want)
		}
	}
}
