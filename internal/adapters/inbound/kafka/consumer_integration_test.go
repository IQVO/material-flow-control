//go:build integration

package kafka

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
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

// tLog sends the consumer's log to t.Log, so a refused dead-letter write is visible in CI (checklist item 10).
type tLog struct{ t *testing.T }

func (w tLog) Write(p []byte) (int, error) {
	w.t.Helper()
	w.t.Log(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}

// seed publishes msgs, retrying while the shared transport's metadata cache does not know the just-created topic yet
// (checklist "Construct the group reader AFTER its topic exists").
func seed(t *testing.T, w *kafkago.Writer, msgs ...kafkago.Message) {
	t.Helper()
	var err error
	for attempt := 0; attempt < 40; attempt++ {
		if err = w.WriteMessages(context.Background(), msgs...); err == nil {
			return
		}
		if !errors.Is(err, kafkago.UnknownTopicOrPartition) && !errors.Is(err, kafkago.LeaderNotAvailable) {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("seed %s: %v", msgs[0].Topic, err)
}

// brokerCase is one consumer proven against a real broker: a poison message, then a good one.
type brokerCase struct {
	name      string
	handlers  func(Facts) map[string]HandlerFunc
	poison    []byte
	poisonKey string
	good      []byte
	goodID    string
}

// assertRealBroker publishes bc's poison and good messages, runs the consumer built by the production constructor
// newConsumer on the test topic (carry (Task 15 review): no throwaway group reader on the production topic, D3),
// and proves: two transient failures are retried, the good message is applied once, the poison one is
// dead-lettered with its key, value and source, and both offsets are committed.
func assertRealBroker(t *testing.T, bc brokerCase) {
	t.Helper()
	// The broker is booted once per test binary by kafkatest, backed by
	// github.com/testcontainers/testcontainers-go/modules/kafka (asrs-control's wording; the fitness sensor
	// TestKafkaIntegrationTestsUseTestcontainers looks for that import path in every Kafka integration test).
	brokers := kafkatest.Brokers(t)
	topic := fmt.Sprintf("it.%s.%d", bc.name, time.Now().UnixNano())
	group := "it-" + topic
	kafkatest.CreateTopic(t, topic)
	kafkatest.CreateTopic(t, topic+DLQSuffix)
	w := &kafkago.Writer{Addr: kafkago.TCP(brokers...), RequiredAcks: kafkago.RequireAll, BatchTimeout: 10 * time.Millisecond}
	defer func() { _ = w.Close() }()
	seed(t, w, kafkago.Message{Topic: topic, Key: []byte(bc.poisonKey), Value: bc.poison}, kafkago.Message{Topic: topic, Key: []byte("good"), Value: bc.good})

	awaitCoordinator(t, brokers, group)
	r := &recorder{failures: 2}
	c := newConsumer(bc.name, brokers, topic, group, bc.handlers(r), slog.New(slog.NewTextHandler(tLog{t}, nil)))
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
	if r.count() != 1 || r.ids[0] != bc.goodID || len(dead) != 1 {
		t.Fatalf("applied %v, dead-lettered %d", r.ids, len(dead))
	}
	// carry (Task 15 review): the dead letter is the poison message itself, with its source and cause.
	cause := testConsumer(bc.handlers(&recorder{})).HandleMessage(context.Background(), bc.poison)
	if dead[0].Key != bc.poisonKey || string(dead[0].Value) != string(bc.poison) || dead[0].Headers["x-dlq-source-topic"] != topic ||
		dead[0].Headers["x-dlq-source-offset"] != "0" || cause == nil || dead[0].Headers["x-dlq-error"] != cause.Error() {
		t.Fatalf("dead letter key %q, value %s, headers %v (want the cause %v)", dead[0].Key, dead[0].Value, dead[0].Headers, cause)
	}

	again := kafkago.NewReader(readerConfig(brokers, topic, group))
	defer func() { _ = again.Close() }()
	short, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	if m, err := again.FetchMessage(short); err == nil {
		t.Fatalf("offset %d was not committed", m.Offset)
	}
}

func TestARealBrokerRetriesATransientFailureCommitsAfterItAndDeadLettersPoison(t *testing.T) {
	assertRealBroker(t, brokerCase{name: "asrs-control-events", handlers: AsrsHandlers,
		poison: event("e-poison", TypeContainerStored, map[string]any{"storage_job_id": "sj-1"}), poisonKey: "sj-1",
		good: event("e-good", TypeContainerStored, map[string]any{"storage_job_id": "sj-2", "asrs_id": "ASRS1", "container_id": "C-1",
			"port_id": "IN1", "stored_at": "2026-11-20T08:00:01Z"}), goodID: "e-good"})
}

// carry (Task 15 review): the amr-fleet-control consumer gets the same real-broker proof.
func TestARealBrokerRetriesCommitsAndDeadLettersAmrFleetControlFacts(t *testing.T) {
	assertRealBroker(t, brokerCase{name: "amr-fleet-control-events", handlers: AmrHandlers,
		poison: event("e-poison", TypeLegAssigned, map[string]any{"leg_id": "leg-1"}), poisonKey: "leg-1",
		good: event("e-good", TypeLegCompleted, map[string]any{"leg_id": "leg-1", "fleet_id": "AMR1", "container_id": "C-1",
			"to_handoff_point_id": "ST1", "robot": map[string]any{"manufacturer": "EMU", "serial_number": "R1"},
			"completed_at": "2026-11-20T08:00:01Z"}), goodID: "e-good"})
}
