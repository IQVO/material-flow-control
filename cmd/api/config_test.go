package main

import (
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func full() map[string]string {
	return map[string]string{"DATABASE_URL": "postgres://u@db:5432/mfc", "KAFKA_BROKERS": "k1:9092,k2:9092",
		"ASRS_CONTROL_URL": "http://asrs-control", "AMR_FLEET_CONTROL_URL": "http://amr-fleet-control", "MFC_LAYOUT_FILE": "/etc/mfc/layout.yaml"}
}

func TestAMissingMandatoryVariableIsABootErrorNamingIt(t *testing.T) {
	for _, name := range []string{"DATABASE_URL", "KAFKA_BROKERS", "ASRS_CONTROL_URL", "AMR_FLEET_CONTROL_URL", "MFC_LAYOUT_FILE"} {
		m := full()
		delete(m, name)
		if _, err := loadConfig(env(m)); err == nil || !strings.Contains(err.Error(), name) {
			t.Fatalf("without %s: %v", name, err)
		}
	}
}

func TestTheDefaultsAreTheDocumentedOnes(t *testing.T) {
	c, err := loadConfig(env(full()))
	if err != nil {
		t.Fatal(err)
	}
	if processDefaultsDiffer(c) || tunableDefaultsDiffer(c) {
		t.Fatalf("config = %+v", c)
	}
}

// The brief's single condition split in two helpers for cyclop (max 15); every comparison is
// byte-identical to the brief's TestTheDefaultsAreTheDocumentedOnes and in the same order.
func processDefaultsDiffer(c config) bool {
	return c.Addr != ":8080" || c.LogLevel != "info" || c.MigrationsURL != "postgres://u@db:5432/mfc" || len(c.Brokers) != 2 ||
		c.AsrsGroup != "material-flow-control.asrs-control-events" || c.AmrGroup != "material-flow-control.amr-fleet-control-events" ||
		c.OutboxInterval != 250*time.Millisecond || c.OutboxBatch != 100 || c.OutboxMaxAttempts != 20
}

func tunableDefaultsDiffer(c config) bool {
	return c.UseCases.LegRetryLimit != 2 || c.UseCases.LegRequestDeadline != 300*time.Second || c.UseCases.LegTimeout != 900*time.Second ||
		c.SubsystemTimeout != 5*time.Second || c.DispatchPoll != 200*time.Millisecond || c.TickInterval != time.Second ||
		c.ServiceName != "material-flow-control" || c.OTLPEndpoint != ""
}

func TestANonNumericTunableIsABootError(t *testing.T) {
	m := full()
	m["LEG_TIMEOUT_SECONDS"] = "soon"
	if _, err := loadConfig(env(m)); err == nil || !strings.Contains(err.Error(), "LEG_TIMEOUT_SECONDS") {
		t.Fatalf("err = %v", err)
	}
	m = full()
	m["LEG_RETRY_LIMIT"] = "-1"
	if _, err := loadConfig(env(m)); err == nil || !strings.Contains(err.Error(), "LEG_RETRY_LIMIT") {
		t.Fatalf("negative = %v", err)
	}
}

// carry (Task 19 review): an interval, timeout or deadline of 0 is a boot error naming it (time.NewTicker
// panics on 0; an http.Client Timeout of 0 means no timeout; a 0 leg timeout fails every leg).
func TestAZeroIntervalOrTimeoutIsABootError(t *testing.T) {
	for _, name := range []string{"OUTBOX_INTERVAL_MS", "SUBSYSTEM_TIMEOUT_MS", "DISPATCH_POLL_MS", "TICK_INTERVAL_MS",
		"LEG_REQUEST_DEADLINE_SECONDS", "LEG_TIMEOUT_SECONDS", "OUTBOX_BATCH"} {
		m := full()
		m[name] = "0"
		if _, err := loadConfig(env(m)); err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("%s=0: err = %v, want a boot error naming it", name, err)
		}
	}
}

// ruling P4-R14: OUTBOX_MAX_ATTEMPTS=0 would disable the relay's attempt cap, so it is a boot error; a
// LEG_RETRY_LIMIT of 0 (never retry a failed leg) stays legitimate.
func TestAZeroAttemptCapIsABootErrorAndAZeroRetryLimitIsNot(t *testing.T) {
	m := full()
	m["OUTBOX_MAX_ATTEMPTS"] = "0"
	if _, err := loadConfig(env(m)); err == nil || !strings.Contains(err.Error(), "OUTBOX_MAX_ATTEMPTS must be a positive integer") {
		t.Fatalf("OUTBOX_MAX_ATTEMPTS=0: err = %v, want a boot error naming it", err)
	}
	m = full()
	m["LEG_RETRY_LIMIT"] = "0"
	if c, err := loadConfig(env(m)); err != nil || c.UseCases.LegRetryLimit != 0 {
		t.Fatalf("LEG_RETRY_LIMIT=0: %+v, %v, want accepted as 0", c.UseCases, err)
	}
}
