// Package kafka holds the outbound Kafka adapters. The outbox relay publishes
// through the Writer built here; the topic is chosen per message, never on the
// Writer, so one Writer serves the events and analytics topics.
package kafka

import (
	"strings"
	"time"

	kafkago "github.com/segmentio/kafka-go"
)

// batchTimeout is the writer's BatchTimeout. The outbox relay sends one row per
// WriteMessages call and keeps its claim transaction open for the whole pass, so
// kafka-go's default (1s, waiting for a batch of 100 that never fills) was paid per
// ROW: the relay drained one event per second, the e2e's 66 rows took over a minute
// and its read window closed before the HELD event's turn (CI, 2026-11-20 — the
// "wedge" goroutine snapshots were healthy passes between one-second sends). 10ms
// flushes a lone row at once and still batches a concurrent burst.
const batchTimeout = 10 * time.Millisecond

// NewWriter builds the shared Kafka writer. RequiredAcks is All and the key
// balancer is Hash, so every event of one aggregate lands on one partition in
// order (the aggregate id is the message key). AllowAutoTopicCreation matches
// the broker, which creates business topics on first write. WriteTimeout and
// ReadTimeout bound a stalled broker connection, and MaxAttempts 2 bounds
// kafka-go's internal retries of one batch (the default 10, each with the full
// request deadline), so a failing send surfaces as an error within the relay
// sink's deadline: the row's failure is recorded and the next pass retries.
func NewWriter(brokers string) *kafkago.Writer {
	return &kafkago.Writer{
		Addr:                   kafkago.TCP(splitBrokers(brokers)...),
		RequiredAcks:           kafkago.RequireAll,
		Balancer:               &kafkago.Hash{},
		AllowAutoTopicCreation: true,
		WriteTimeout:           10 * time.Second,
		ReadTimeout:            10 * time.Second,
		MaxAttempts:            2,
		BatchTimeout:           batchTimeout,
	}
}

func splitBrokers(s string) []string {
	var out []string
	for _, b := range strings.Split(s, ",") {
		if b = strings.TrimSpace(b); b != "" {
			out = append(out, b)
		}
	}
	return out
}
