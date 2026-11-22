//go:build integration

package kafka_test

import (
	"context"
	"testing"
	"time"

	"github.com/IQVO/material-flow-control/internal/adapters/outbound/kafka"
	"github.com/IQVO/material-flow-control/internal/application/outbox"
	"github.com/IQVO/material-flow-control/internal/testing/kafkatest"
)

// TestRelaySinkSendsOneRowWithoutWaitingForABatch pins the relay's throughput against a
// real broker. The outbox relay sends ONE row per Send (one WriteMessages call) and holds
// its claim transaction until the whole pass is sent, so a writer that waits for a batch
// to fill before flushing turns every row into a fixed delay: with kafka-go's default
// BatchTimeout (1s) the e2e's 66 rows took 66s to drain and its 60s read window closed
// before the HELD event's turn — no wedge, just one event per second.
func TestRelaySinkSendsOneRowWithoutWaitingForABatch(t *testing.T) {
	const topic = "latency.itest.events"
	kafkatest.CreateTopic(t, topic)
	sink := kafka.NewRelaySink(kafkatest.Brokers(t))
	t.Cleanup(func() { _ = sink.Close() })
	msg := outbox.Message{Topic: topic, Key: []byte("k"), Value: []byte(`{}`)}

	// The first send pays the metadata lookup and the dial; it is not what is measured.
	if err := sink.Send(context.Background(), msg); err != nil {
		t.Fatalf("warm-up send: %v", err)
	}
	const rows = 20
	start := time.Now()
	for i := 0; i < rows; i++ {
		if err := sink.Send(context.Background(), msg); err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
	}
	// 20 single-row sends against a local broker take well under a second when each
	// flushes at once; a 1s batch wait per row makes it ~20s.
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("%d single-row sends took %s (%s per row): the writer waits for a batch before flushing", rows, took, took/rows)
	}
}
