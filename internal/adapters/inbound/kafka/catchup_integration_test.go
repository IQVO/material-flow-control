//go:build integration

package kafka

import (
	"context"
	"strconv"
	"testing"
	"time"

	kafkago "github.com/segmentio/kafka-go"

	"github.com/IQVO/material-flow-control/internal/testing/kafkatest"
)

// ruling P4-R20: the gate's broker reads against a real broker: a topic's retained range, an unknown topic (nothing
// to replay), and a group's committed offset (-1 before, the next offset after a commit).
func TestTheBrokerOffsetsTheCatchUpGateCompares(t *testing.T) {
	// The broker is booted once per test binary by kafkatest, backed by
	// github.com/testcontainers/testcontainers-go/modules/kafka (the fitness sensor
	// TestKafkaIntegrationTestsUseTestcontainers looks for that import path in every Kafka integration test).
	brokers := kafkatest.Brokers(t)
	nano := strconv.FormatInt(time.Now().UnixNano(), 36)
	topic := "catchup.offsets." + nano
	kafkatest.CreateTopic(t, topic)
	kafkatest.Produce(t, topic, "a", "b", "c")
	src := brokerOffsets{client: &kafkago.Client{Addr: kafkago.TCP(brokers...), Timeout: 10 * time.Second}}
	ctx := context.Background()

	ends, err := src.endOffsets(ctx, topic)
	if err != nil || len(ends) != 1 || ends[0] != (partitionEnd{first: 0, last: 3}) {
		t.Fatalf("endOffsets(%s) = %v, %v; want partition 0 [0, 3)", topic, ends, err)
	}
	if none, err := src.endOffsets(ctx, "catchup.never-created."+nano); err != nil || len(none) != 0 {
		t.Fatalf("endOffsets(unknown topic) = %v, %v; want no partitions", none, err)
	}
	group := "catchup-offsets-" + nano
	awaitCoordinator(t, brokers, group)
	var before map[int]int64
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if before, err = src.committedOffsets(ctx, group, topic, []int{0}); err == nil {
			break
		}
	}
	if err != nil || before[0] != -1 {
		t.Fatalf("committedOffsets before a commit = %v, %v; want -1", before, err)
	}
	r := kafkago.NewReader(readerConfig(brokers, topic, group))
	defer func() { _ = r.Close() }()
	rctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	var last kafkago.Message
	for range 3 {
		if last, err = r.FetchMessage(rctx); err != nil {
			t.Fatalf("fetch: %v", err)
		}
	}
	if err := r.CommitMessages(rctx, last); err != nil {
		t.Fatalf("commit: %v", err)
	}
	after, err := src.committedOffsets(ctx, group, topic, []int{0})
	if err != nil || after[0] != 3 {
		t.Fatalf("committedOffsets after committing offset 2 = %v, %v; want 3", after, err)
	}
}
