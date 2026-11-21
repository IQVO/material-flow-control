//go:build integration

package kafka

import (
	"context"
	"fmt"
	"testing"
	"time"

	kafkago "github.com/segmentio/kafka-go"

	"github.com/IQVO/material-flow-control/internal/testing/kafkatest"
)

// awaitCoordinator blocks until the broker answers FindCoordinator for group (checklist "Cold broker").
func awaitCoordinator(t *testing.T, brokers []string, group string) {
	t.Helper()
	client := &kafkago.Client{Addr: kafkago.TCP(brokers...), Timeout: 5 * time.Second}
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.FindCoordinator(context.Background(), &kafkago.FindCoordinatorRequest{Key: group, KeyType: kafkago.CoordinatorKeyTypeConsumer})
		if err == nil && resp.Error == nil {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("no coordinator for %s", group)
}

func TestARealBrokerRetriesATransientFailureCommitsAfterItAndDeadLettersPoison(t *testing.T) {
	// The broker is booted once per test binary by kafkatest, backed by
	// github.com/testcontainers/testcontainers-go/modules/kafka (asrs-control's wording; the fitness sensor
	// TestKafkaIntegrationTestsUseTestcontainers looks for that import path in every Kafka integration test).
	brokers := kafkatest.Brokers(t)
	topic := fmt.Sprintf("it.asrs-control.events.%d", time.Now().UnixNano())
	group := "it-" + topic
	kafkatest.CreateTopic(t, topic)
	kafkatest.CreateTopic(t, topic+DLQSuffix)
	w := &kafkago.Writer{Addr: kafkago.TCP(brokers...), RequiredAcks: kafkago.RequireAll, BatchTimeout: 10 * time.Millisecond}
	defer func() { _ = w.Close() }()
	poison := event("e-poison", TypeContainerStored, map[string]any{"storage_job_id": "sj-1"})
	good := event("e-good", TypeContainerStored, map[string]any{"storage_job_id": "sj-2", "asrs_id": "ASRS1", "container_id": "C-1",
		"port_id": "IN1", "stored_at": "2026-11-20T08:00:01Z"})
	if err := w.WriteMessages(context.Background(), kafkago.Message{Topic: topic, Key: []byte("sj-1"), Value: poison},
		kafkago.Message{Topic: topic, Key: []byte("sj-2"), Value: good}); err != nil {
		t.Fatal(err)
	}

	awaitCoordinator(t, brokers, group)
	r := &recorder{failures: 2}
	c := NewAsrsConsumer(brokers, group, r, quiet())
	// The constructor's reader already joined group on AsrsTopic: leave the group before the test topic's
	// reader joins, or the two members rebalance against each other and the test topic is never consumed.
	if err := c.Reader.Close(); err != nil {
		t.Fatal(err)
	}
	c.Topic, c.Reader = topic, kafkago.NewReader(readerConfig(brokers, topic, group))
	c.Retry = RetryPolicy{Initial: 10 * time.Millisecond, Max: 50 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()

	dead := kafkatest.ReadAll(t, topic+DLQSuffix, func(m []kafkatest.Message) bool { return len(m) >= 1 }, 60*time.Second)
	deadline := time.Now().Add(60 * time.Second)
	for r.count() < 1 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	<-done
	_ = c.Close()
	if r.count() != 1 || r.ids[0] != "e-good" || len(dead) != 1 {
		t.Fatalf("applied %v, dead-lettered %d", r.ids, len(dead))
	}

	again := kafkago.NewReader(readerConfig(brokers, topic, group))
	defer func() { _ = again.Close() }()
	short, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	if m, err := again.FetchMessage(short); err == nil {
		t.Fatalf("offset %d was not committed", m.Offset)
	}
}
