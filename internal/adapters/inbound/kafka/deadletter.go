// Dead-letter handling for the domain consumers (carry (Task 15 review): the three paths):
//   - a message that is not a CloudEvents 1.0 message, or whose type this
//     consumer does not act on, is committed past: HandleMessage returns nil
//     and it is never dead-lettered;
//   - a message that can never be applied (a malformed payload, a missing
//     required field, a value outside its enum) is errPoison: it is published
//     to "<topic>.dlq" with x-dlq-* headers AT ONCE, without a retry, then
//     committed past;
//   - a TRANSIENT failure is retried with capped backoff (consumeLoop's
//     policy) and dead-lettered only after domainMaxHandlerAttempts attempts,
//     instead of blocking the partition forever.
//
// The dead-letter publish itself is retried until it succeeds or ctx ends,
// and the offset is committed only after it.
package kafka

import (
	"context"
	"errors"
	"strconv"
	"time"

	kafkago "github.com/segmentio/kafka-go"
)

// DLQSuffix is appended to a consumed topic to name its dead-letter topic
// (warehouse.asrs-control.events.dlq for the asrs-control consumer).
const DLQSuffix = ".dlq"

// DeadLetterWriter is the subset of *kafkago.Writer a dead-lettering
// consumer needs, so unit tests never need a broker.
type DeadLetterWriter interface {
	WriteMessages(ctx context.Context, msgs ...kafkago.Message) error
	Close() error
}

// Bounded retry while an auto-created DLQ topic has no leader yet.
const (
	dlqTopicReadyAttempts = 40
	dlqTopicReadyBackoff  = 250 * time.Millisecond
)

// writeDLQ publishes msg, retrying (bounded) while the auto-created DLQ topic
// has no leader yet. Any other error, or exhausting the budget, is returned.
func writeDLQ(ctx context.Context, w DeadLetterWriter, msg kafkago.Message) error {
	var err error
	for attempt := 0; attempt < dlqTopicReadyAttempts; attempt++ {
		if err = w.WriteMessages(ctx, msg); err == nil || !isTopicNotReady(err) {
			return err
		}
		select {
		case <-ctx.Done():
			return errors.Join(err, ctx.Err())
		case <-time.After(dlqTopicReadyBackoff):
		}
	}
	return err
}

// isTopicNotReady reports whether err only says the topic (or its leader)
// does not exist yet: the transient state right after auto-creation.
func isTopicNotReady(err error) bool {
	var werrs kafkago.WriteErrors
	if errors.As(err, &werrs) {
		for _, e := range werrs {
			if e != nil && !isTopicNotReady(e) {
				return false
			}
		}
		return werrs.Count() > 0
	}
	return errors.Is(err, kafkago.UnknownTopicOrPartition) || errors.Is(err, kafkago.LeaderNotAvailable)
}

// domainMaxHandlerAttempts bounds a domain consumer's in-loop retry of a
// transient failure before the message is dead-lettered: 1 initial
// attempt plus up to 4 retries. Chosen (rather than the fleet's smaller
// maxHandlerAttempts=3 used by order-management/fulfillment-execution,
// which retry inside ONE handler call) to stay comfortably above this
// package's existing "fails twice then succeeds" test fixtures while
// still bounding the previously-infinite retry.
const domainMaxHandlerAttempts = 5

// newDomainDLQWriter builds the dead-letter writer of a domain Kafka
// consumer. It follows the SAME sync-writer settings as this service's
// outbox relay (outbound/kafka.RelaySink): a Hash balancer on the message
// key (so one key's dead-lettered messages land on one partition,
// preserving relative order for that key), RequireAll acks,
// AllowAutoTopicCreation (the DLQ topic has never been written to before
// the first poison message) and a short BatchTimeout so a lone synchronous
// write is not held for kafka-go's 1s default. WriteTimeout and ReadTimeout
// are the fleet's 10 s (kafka_writer_timeout_fitness_test): with RequireAll and
// no deadline one half-open broker connection blocks the dead-letter write
// forever, wedging the partition.
//
// The writer has NO fixed Topic: publishDeadLetter names "<topic>.dlq" on
// every message, and kafka-go refuses a message whose Topic is set when the
// Writer's is too ("Topic must not be specified for both Writer and
// Message"), which retried the dead-letter publish forever and wedged the
// partition (a fleet defect found in customer-returns, checklist item 10).
func newDomainDLQWriter(brokers []string) DeadLetterWriter {
	return &kafkago.Writer{
		Addr:                   kafkago.TCP(brokers...),
		Balancer:               &kafkago.Hash{},
		RequiredAcks:           kafkago.RequireAll,
		BatchTimeout:           10 * time.Millisecond,
		WriteTimeout:           10 * time.Second,
		ReadTimeout:            10 * time.Second,
		AllowAutoTopicCreation: true,
	}
}

// publishDeadLetter publishes msg (its original key/value/headers, plus
// x-dlq-source-topic/x-dlq-error/x-dlq-failed-at context) to dlqTopic via
// w, retrying while the auto-created topic has no leader yet (writeDLQ).
// The source topic/partition/offset are
// taken from msg itself, so callers never need to pass them separately.
func publishDeadLetter(ctx context.Context, w DeadLetterWriter, dlqTopic string, msg kafkago.Message, cause error) error {
	headers := append([]kafkago.Header{}, msg.Headers...)
	headers = append(headers,
		kafkago.Header{Key: "x-dlq-source-topic", Value: []byte(msg.Topic)},
		kafkago.Header{Key: "x-dlq-source-partition", Value: []byte(strconv.Itoa(msg.Partition))},
		kafkago.Header{Key: "x-dlq-source-offset", Value: []byte(strconv.FormatInt(msg.Offset, 10))},
		kafkago.Header{Key: "x-dlq-error", Value: []byte(cause.Error())},
		kafkago.Header{Key: "x-dlq-failed-at", Value: []byte(time.Now().UTC().Format(time.RFC3339))},
	)
	return writeDLQ(ctx, w, kafkago.Message{Topic: dlqTopic, Key: msg.Key, Value: msg.Value, Headers: headers})
}
