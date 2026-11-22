package kafka

import (
	"context"
	"fmt"
	"time"

	kafkago "github.com/segmentio/kafka-go"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/IQVO/material-flow-control/internal/application/outbox"
)

// Writer is the subset of *kafkago.Writer the relay sink needs.
type Writer interface {
	WriteMessages(ctx context.Context, msgs ...kafkago.Message) error
}

// sendTimeout bounds one relay send. kafka-go's dial, resolve and batch paths
// carry deadlines of their own only some of the time; this ctx deadline is the
// backstop that keeps a stalled send from wedging the relay's Drain pass.
const sendTimeout = 15 * time.Second

// RelaySink is the outbox relay's ports-facing Sink: it writes each drained
// outbox message to the topic the message itself names (the writer built by
// NewWriter is topic-less, so one sink serves the events and the analytics
// topics).
type RelaySink struct {
	writer Writer
}

// NewRelaySink constructs a RelaySink over brokers. It does not dial.
func NewRelaySink(brokers []string) *RelaySink {
	w := NewWriter(join(brokers))
	return &RelaySink{writer: w}
}

// NewRelaySinkWithWriter constructs a RelaySink over an explicit Writer (a
// fake in tests). The writer must NOT have a Topic set.
func NewRelaySinkWithWriter(w Writer) *RelaySink { return &RelaySink{writer: w} }

// Send writes msgs in one WriteMessages call. A message without a topic is
// rejected before anything is written. The write is bounded: WriteMessages
// honours ctx cancellation, so a client-side stall (an undeadlined dial, a DNS
// lookup, a wedged batch — all seen in CI on the runner's container networking)
// surfaces as an error here after sendTimeout instead of blocking the relay's
// Drain forever with its claimed outbox rows locked (the 2026-11-20 e2e
// incident: rows rotted unpublished with zero error logs). The Drain pass then
// records the failure, commits — releasing the row locks — and the next pass
// retries; delivery stays at-least-once with the CloudEvents id persisted at
// encode time.
func (s *RelaySink) Send(ctx context.Context, msgs ...outbox.Message) error {
	if len(msgs) == 0 {
		return nil
	}
	for i, m := range msgs {
		if m.Topic == "" {
			return fmt.Errorf("kafka relay sink: message %d (%s) has no topic", i, m.EventType)
		}
	}
	out := make([]kafkago.Message, len(msgs))
	spans := make([]trace.Span, len(msgs))
	for i, m := range msgs {
		headers := kafkaHeaders(m.Headers)
		spans[i] = startPublish(ctx, m, &headers)
		out[i] = kafkago.Message{Topic: m.Topic, Key: m.Key, Value: m.Value, Headers: headers}
	}
	bctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	err := s.writer.WriteMessages(bctx, out...)
	for _, span := range spans {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}
	if err != nil {
		return fmt.Errorf("kafka relay sink: write %d message(s): %w", len(out), err)
	}
	return nil
}

// Close releases the underlying Kafka writer.
func (s *RelaySink) Close() error {
	if w, ok := s.writer.(*kafkago.Writer); ok {
		return w.Close()
	}
	return nil
}

// join renders brokers the way NewWriter wants (comma-separated).
func join(brokers []string) string {
	out := ""
	for i, b := range brokers {
		if i > 0 {
			out += ","
		}
		out += b
	}
	return out
}
