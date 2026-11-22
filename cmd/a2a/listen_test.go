package main

import (
	"errors"
	"io"
	"net"
	"strings"
	"syscall"
	"testing"
)

// deadDB is a Postgres DSN nothing listens on: the boot migrations fail at once.
const deadDB = "postgres://mfc@127.0.0.1:1/mfc?sslmode=disable&connect_timeout=1"

func bootEnv(addr string) func(string) string {
	return env(map[string]string{"A2A_ADDR": addr, "DATABASE_URL": deadDB, "MFC_LAYOUT_FILE": "../../config/flow-layout.example.yaml", "A2A_AGENT_URL": testAgentURL})
}

// carry (Task 20 review): the listener is bound BEFORE buildReads, so a busy address is the boot error (named,
// at once) rather than surfacing only after the migrations and the pool were set up.
func TestABusyAddressIsTheBootErrorBeforeAnyRead(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = busy.Close() }()
	err = run(io.Discard, bootEnv(busy.Addr().String()))
	if !errors.Is(err, syscall.EADDRINUSE) || strings.Contains(err.Error(), "migrations") {
		t.Fatalf("boot on a busy address = %v, want the bind error before any read is wired", err)
	}
}

// carry (Task 20 review): when buildReads fails after the bind, the listener is closed (the port is free again).
func TestAFailedBuildReadsClosesTheListener(t *testing.T) {
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := probe.Addr().String()
	_ = probe.Close()
	if err := run(io.Discard, bootEnv(addr)); err == nil || !strings.Contains(err.Error(), "migrations") {
		t.Fatalf("boot against a dead database = %v, want the migrations error", err)
	}
	again, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("the listener on %s was left open after the failed boot: %v", addr, err)
	}
	_ = again.Close()
}
