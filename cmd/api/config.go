package main

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/IQVO/material-flow-control/internal/adapters/outbound/telemetry"
	"github.com/IQVO/material-flow-control/internal/application/usecases"
)

// config is the api's whole environment (Task 19's list; nothing else is read).
type config struct {
	DatabaseURL, MigrationsURL, Addr, LogLevel string
	Brokers                                    []string
	AsrsURL, AmrURL, LayoutFile                string
	AsrsGroup, AmrGroup                        string
	OutboxInterval                             time.Duration
	OutboxBatch, OutboxMaxAttempts             int
	SubsystemTimeout, DispatchPoll             time.Duration
	TickInterval                               time.Duration
	UseCases                                   usecases.Config
	ServiceName, ServiceVersion, OTLPEndpoint  string
	Retention                                  retention
}

// retention is the purge policy of the operational tables (ADR 0008): how long each kind of terminal row is kept,
// and the rows one purge batch deletes.
type retention struct {
	Orders, Moves, Commands, Outbox time.Duration
	Batch                           int
}

// minOrderRetentionHours is the 24-hour idempotency window: a WES retries a transport_request_id within it, and a
// retry after the record was purged would be accepted again and move the container twice.
const minOrderRetentionHours = 24

type reader struct {
	getenv func(string) string
	errs   []error
}

func (r *reader) str(name, def string) string {
	if v := strings.TrimSpace(r.getenv(name)); v != "" {
		return v
	}
	return def
}

func (r *reader) must(name string) string {
	v := r.str(name, "")
	if v == "" {
		r.errs = append(r.errs, fmt.Errorf("%s is required", name))
	}
	return v
}

func (r *reader) num(name string, def int) int {
	v := r.str(name, "")
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		r.errs = append(r.errs, fmt.Errorf("%s must be a non-negative integer, got %q", name, v))
		return def
	}
	return n
}

// pos is num for a value that must be at least 1: an interval, timeout, deadline, batch or attempt cap of 0
// would panic a ticker, disable a timeout or a cap, or end every leg at once. carry (Task 19 review);
// ruling P4-R14 adds OUTBOX_MAX_ATTEMPTS.
func (r *reader) pos(name string, def int) int {
	v := r.str(name, "")
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		r.errs = append(r.errs, fmt.Errorf("%s must be a positive integer, got %q", name, v))
		return def
	}
	return n
}

// hours is a duration in hours of at least floor (1 = pos).
func (r *reader) hours(name string, def, floor int) time.Duration {
	v := r.str(name, "")
	if v == "" {
		return time.Duration(def) * time.Hour
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < floor {
		r.errs = append(r.errs, fmt.Errorf("%s must be a whole number of hours of at least %d, got %q", name, floor, v))
		return time.Duration(def) * time.Hour
	}
	return time.Duration(n) * time.Hour
}

// dur is a positive duration in unit (pos).
func (r *reader) dur(name string, def int, unit time.Duration) time.Duration {
	return time.Duration(r.pos(name, def)) * unit
}

func loadConfig(getenv func(string) string) (config, error) {
	r := &reader{getenv: getenv}
	c := config{DatabaseURL: r.must("DATABASE_URL"), AsrsURL: r.must("ASRS_CONTROL_URL"), AmrURL: r.must("AMR_FLEET_CONTROL_URL"),
		LayoutFile: r.must("MFC_LAYOUT_FILE"), Addr: r.str("ADDR", ":8080"), LogLevel: r.str("LOG_LEVEL", "info"),
		AsrsGroup:      r.str("MFC_ASRS_CONSUMER_GROUP", "material-flow-control.asrs-control-events"),
		AmrGroup:       r.str("MFC_AMR_CONSUMER_GROUP", "material-flow-control.amr-fleet-control-events"),
		OutboxInterval: r.dur("OUTBOX_INTERVAL_MS", 250, time.Millisecond), OutboxBatch: r.pos("OUTBOX_BATCH", 100),
		OutboxMaxAttempts: r.pos("OUTBOX_MAX_ATTEMPTS", 20), SubsystemTimeout: r.dur("SUBSYSTEM_TIMEOUT_MS", 5000, time.Millisecond),
		DispatchPoll: r.dur("DISPATCH_POLL_MS", 200, time.Millisecond), TickInterval: r.dur("TICK_INTERVAL_MS", 1000, time.Millisecond),
		ServiceName: r.str("OTEL_SERVICE_NAME", "material-flow-control"), ServiceVersion: r.str("SERVICE_VERSION", ""),
		OTLPEndpoint: r.str("OTEL_EXPORTER_OTLP_ENDPOINT", "")}
	for _, b := range strings.Split(r.must("KAFKA_BROKERS"), ",") {
		if b = strings.TrimSpace(b); b != "" {
			c.Brokers = append(c.Brokers, b)
		}
	}
	c.MigrationsURL = r.str("MIGRATIONS_DATABASE_URL", c.DatabaseURL)
	c.UseCases = usecases.DefaultConfig()
	c.UseCases.LegRetryLimit = r.num("LEG_RETRY_LIMIT", c.UseCases.LegRetryLimit)
	c.UseCases.LegRequestDeadline = r.dur("LEG_REQUEST_DEADLINE_SECONDS", 300, time.Second)
	c.UseCases.LegTimeout = r.dur("LEG_TIMEOUT_SECONDS", 900, time.Second)
	c.Retention = retention{Orders: r.hours("ORDER_RETENTION_HOURS", 720, minOrderRetentionHours),
		Moves: r.hours("CONTAINER_MOVE_RETENTION_HOURS", 720, 1), Commands: r.hours("COMMAND_RETENTION_HOURS", 720, 1),
		Outbox: r.hours("OUTBOX_RETENTION_HOURS", 168, 1), Batch: r.pos("PURGE_BATCH", 1000)}
	// carry (plan 03 Task 16 review parity): a set-but-unknown LOG_LEVEL is a boot error, never a silent info.
	if _, err := telemetry.ParseLogLevel(c.LogLevel); err != nil {
		r.errs = append(r.errs, err)
	}
	if err := errors.Join(r.errs...); err != nil {
		return config{}, fmt.Errorf("material-flow-control config: %w", err)
	}
	return c, nil
}
