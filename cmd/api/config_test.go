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
