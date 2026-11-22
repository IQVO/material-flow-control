//go:build integration

// Package kafkatest boots ONE Kafka broker per test binary (testcontainers). Topic
// auto-creation is OFF (the fleet convention, as in outbound-shipping's kafkatest): confluent
// local auto-creates a topic on first produce with an unverified partition count, so a reader
// of partition 0 can silently miss messages that hashed elsewhere. A test creates its topics
// explicitly with CreateTopic (one partition, leader awaited) before the service writes.
package kafkatest

import (
	"context"
	"errors"
	"net"
	"os"
	"runtime/pprof"
	"sync"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	kafkago "github.com/segmentio/kafka-go"
	"github.com/testcontainers/testcontainers-go"
	tckafka "github.com/testcontainers/testcontainers-go/modules/kafka"
	"github.com/testcontainers/testcontainers-go/network"

	"github.com/IQVO/material-flow-control/internal/testing/dockernet"
)

var (
	once     sync.Once
	brokers  []string
	errStart error
)

// Brokers starts the broker once and returns its addresses.
func Brokers(t *testing.T) []string {
	t.Helper()
	once.Do(func() {
		ctx := context.Background()
		nw, err := dockernet.Get()
		if err != nil {
			errStart = err
			return
		}
		// The broker's hostname is "kafka": the module advertises its BROKER listener as <hostname>:9092, which a
		// container on the shared network reaches through the alias (InternalBrokers).
		c, err := tckafka.Run(ctx, "confluentinc/confluent-local:7.6.1", tckafka.WithClusterID("asrs-control-itest"),
			testcontainers.WithEnv(map[string]string{"KAFKA_AUTO_CREATE_TOPICS_ENABLE": "false"}),
			network.WithNetwork([]string{internalHost}, nw),
			testcontainers.WithConfigModifier(func(c *container.Config) { c.Hostname = internalHost }))
		if err != nil {
			errStart = err
			return
		}
		brokers, errStart = c.Brokers(ctx)
		// Rewrite localhost to 127.0.0.1: the module returns "localhost:<mapped>",
		// and one CI run hung forever inside net.Resolver.lookupIPAddr resolving
		// "localhost" during the writer's LookupPartition — an undeadlined DNS
		// lookup that held the outbox relay's row locks with it. IP literals skip
		// the resolver entirely.
		for i, b := range brokers {
			if host, port, err := net.SplitHostPort(b); err == nil && (host == "localhost" || host == "") {
				brokers[i] = net.JoinHostPort("127.0.0.1", port)
			}
		}
	})
	if errStart != nil {
		t.Fatalf("start kafka: %v", errStart)
	}
	return brokers
}

// internalHost is the broker's name on the dockernet network.
const internalHost = "kafka"

// InternalBrokers is the broker's address for a CONTAINER on the dockernet network (its BROKER listener); the test
// process itself uses Brokers.
func InternalBrokers(t *testing.T) []string {
	t.Helper()
	Brokers(t)
	return []string{internalHost + ":9092"}
}

// CreateTopic creates a one-partition topic and waits for its leader (CreateTopics returns
// before the broker is done; producing or reading before the leader exists is the
// unknown-topic race that once lost this suite its first CI run).
func CreateTopic(t *testing.T, topic string) {
	t.Helper()
	conn, err := kafkago.Dial("tcp", Brokers(t)[0])
	if err != nil {
		t.Fatalf("dial broker: %v", err)
	}
	defer func() { _ = conn.Close() }()
	if err := conn.CreateTopics(kafkago.TopicConfig{Topic: topic, NumPartitions: 1, ReplicationFactor: 1}); err != nil {
		t.Fatalf("create topic %s: %v", topic, err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if parts, err := conn.ReadPartitions(topic); err == nil && len(parts) == 1 && parts[0].Leader.ID != 0 {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("topic %s never got a leader", topic)
}

// Message is one record read back.
type Message struct {
	Key     string
	Value   []byte
	Headers map[string]string
}

// ReadAll reads topic partition 0 from the start until until(read) is true or timeout passes.
func ReadAll(t *testing.T, topic string, until func([]Message) bool, timeout time.Duration) []Message {
	t.Helper()
	r := kafkago.NewReader(kafkago.ReaderConfig{Brokers: Brokers(t), Topic: topic, Partition: 0, MinBytes: 1, MaxBytes: 1 << 20})
	defer func() { _ = r.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var out []Message
	for !until(out) {
		m, err := r.ReadMessage(ctx)
		if errors.Is(err, context.DeadlineExceeded) {
			// Name the hang, wherever it is: the relay/sender has wedged with row locks
			// held in other CI rounds and no error ever surfaced.
			_ = pprof.Lookup("goroutine").WriteTo(os.Stderr, 2)
			keys := make([]string, len(out))
			for i, m := range out {
				keys[i] = m.Key
			}
			t.Fatalf("read %s: %d messages before the timeout, the condition never held; keys read: %v", topic, len(out), keys)
		}
		if err != nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		h := map[string]string{}
		for _, x := range m.Headers {
			h[x.Key] = string(x.Value)
		}
		out = append(out, Message{Key: string(m.Key), Value: m.Value, Headers: h})
	}
	return out
}
