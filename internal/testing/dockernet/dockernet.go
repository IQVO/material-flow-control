//go:build integration

// Package dockernet holds the ONE Docker network of a test binary. pgtest's Postgres (alias "postgres") and kafkatest's
// broker (hostname and alias "kafka") join it, so a subsystem that runs as a CONTAINER (the e2e test's asrs-control and
// amr-fleet-control release images) reaches them by name, while the test process keeps the mapped 127.0.0.1 ports.
package dockernet

import (
	"context"
	"sync"
	"testing"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/network"
)

var (
	once   sync.Once
	nw     *testcontainers.DockerNetwork
	errNew error
)

// Get creates the network once (testcontainers' reaper removes it with the session) and returns it.
func Get() (*testcontainers.DockerNetwork, error) {
	once.Do(func() { nw, errNew = network.New(context.Background()) })
	return nw, errNew
}

// Name is the network's name for a container request's Networks.
func Name(t *testing.T) string {
	t.Helper()
	n, err := Get()
	if err != nil {
		t.Fatalf("create the test network: %v", err)
	}
	return n.Name
}
