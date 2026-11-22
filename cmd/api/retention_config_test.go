package main

import (
	"strings"
	"testing"
	"time"
)

// Issue #3 (ADR 0008): the retention windows and the purge batch are positive integers with documented defaults; the
// order window may not be shorter than the 24-hour idempotency window.
func TestTheRetentionDefaultsAreTheDocumentedOnes(t *testing.T) {
	c, err := loadConfig(env(full()))
	if err != nil {
		t.Fatal(err)
	}
	want := retention{Orders: 720 * time.Hour, Moves: 720 * time.Hour, Commands: 720 * time.Hour, Outbox: 168 * time.Hour, Batch: 1000}
	if c.Retention != want {
		t.Fatalf("retention = %+v, want %+v", c.Retention, want)
	}
}

func TestTheRetentionVariablesAreRead(t *testing.T) {
	m := full()
	m["ORDER_RETENTION_HOURS"], m["CONTAINER_MOVE_RETENTION_HOURS"], m["COMMAND_RETENTION_HOURS"] = "48", "72", "96"
	m["OUTBOX_RETENTION_HOURS"], m["PURGE_BATCH"] = "1", "50"
	c, err := loadConfig(env(m))
	if err != nil {
		t.Fatal(err)
	}
	want := retention{Orders: 48 * time.Hour, Moves: 72 * time.Hour, Commands: 96 * time.Hour, Outbox: time.Hour, Batch: 50}
	if c.Retention != want {
		t.Fatalf("retention = %+v, want %+v", c.Retention, want)
	}
}

func TestAZeroNegativeOrNonNumericRetentionIsABootErrorNamingIt(t *testing.T) {
	for _, name := range []string{"ORDER_RETENTION_HOURS", "CONTAINER_MOVE_RETENTION_HOURS", "COMMAND_RETENTION_HOURS",
		"OUTBOX_RETENTION_HOURS", "PURGE_BATCH"} {
		for _, v := range []string{"0", "-1", "a week"} {
			m := full()
			m[name] = v
			if _, err := loadConfig(env(m)); err == nil || !strings.Contains(err.Error(), name) {
				t.Errorf("%s=%q: err = %v, want a boot error naming it", name, v, err)
			}
		}
	}
}

// A transport_request_id retried after its record was purged would be accepted again and move the container a
// second time: the order window never undercuts the 24-hour window a WES retries in.
func TestAnOrderRetentionShorterThanTheIdempotencyWindowIsABootError(t *testing.T) {
	m := full()
	m["ORDER_RETENTION_HOURS"] = "23"
	if _, err := loadConfig(env(m)); err == nil || !strings.Contains(err.Error(), "ORDER_RETENTION_HOURS") ||
		!strings.Contains(err.Error(), "24") {
		t.Fatalf("23h: err = %v, want a boot error naming ORDER_RETENTION_HOURS and the 24-hour floor", err)
	}
	m["ORDER_RETENTION_HOURS"] = "24"
	if c, err := loadConfig(env(m)); err != nil || c.Retention.Orders != 24*time.Hour {
		t.Fatalf("24h: %+v, %v", c.Retention, err)
	}
}

// carry (retention review): a window past maxRetentionHours (ten years) is a boot error naming the variable.
// time.Duration overflows past 2562047 hours, so an unbounded window turned negative and the cutoff into the
// future: every terminal row would be purged at once.
func TestARetentionWindowPastTenYearsIsABootErrorNamingIt(t *testing.T) {
	for _, name := range []string{"ORDER_RETENTION_HOURS", "CONTAINER_MOVE_RETENTION_HOURS", "COMMAND_RETENTION_HOURS",
		"OUTBOX_RETENTION_HOURS"} {
		for _, v := range []string{"87601", "2562048", "9223372036854775807"} {
			m := full()
			m[name] = v
			if _, err := loadConfig(env(m)); err == nil || !strings.Contains(err.Error(), name) ||
				!strings.Contains(err.Error(), "87600") {
				t.Errorf("%s=%q: err = %v, want a boot error naming it and the 87600-hour cap", name, v, err)
			}
		}
		m := full()
		m[name] = "87600"
		if c, err := loadConfig(env(m)); err != nil || c.Retention.Orders+c.Retention.Moves+c.Retention.Commands+
			c.Retention.Outbox <= 0 {
			t.Errorf("%s=87600: %+v, %v; want accepted", name, c.Retention, err)
		}
	}
}
