package telemetry

import (
	"fmt"
	"log/slog"
	"strings"
)

// ParseLogLevel maps a LOG_LEVEL value to its slog level: debug, info, warn (or warning) or error, in any case and
// with surrounding spaces ignored. Anything else is an error naming LOG_LEVEL (and info), so a composition root
// reports a set-but-unknown level as a boot error instead of silently logging at info. carry (plan 03 Task 16
// review parity)
func ParseLogLevel(level string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	return slog.LevelInfo, fmt.Errorf("LOG_LEVEL must be one of debug, info, warn, error, got %q", level)
}
