package kafka

import (
	"context"

	kafkago "github.com/segmentio/kafka-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/IQVO/material-flow-control/internal/application/outbox"
)

// tracerName is the instrumentation scope of the relay's publish spans (ADR 0007).
const tracerName = "github.com/IQVO/material-flow-control/internal/adapters/outbound/kafka"

// headerCarrier reads and replaces the trace-context headers of one message in place.
type headerCarrier struct{ headers *[]kafkago.Header }

var _ propagation.TextMapCarrier = headerCarrier{}

func (c headerCarrier) Get(key string) string {
	for _, h := range *c.headers {
		if h.Key == key {
			return string(h.Value)
		}
	}
	return ""
}

// Set replaces the header (the stored traceparent becomes the publish span's) or appends it.
func (c headerCarrier) Set(key, value string) {
	for i, h := range *c.headers {
		if h.Key == key {
			(*c.headers)[i].Value = []byte(value)
			return
		}
	}
	*c.headers = append(*c.headers, kafkago.Header{Key: key, Value: []byte(value)})
}

func (c headerCarrier) Keys() []string {
	keys := make([]string, 0, len(*c.headers))
	for _, h := range *c.headers {
		keys = append(keys, h.Key)
	}
	return keys
}

// kafkaHeaders copies the stored headers (never aliasing the outbox message's slice).
func kafkaHeaders(in []outbox.Header) []kafkago.Header {
	out := make([]kafkago.Header, len(in))
	for j, h := range in {
		out[j] = kafkago.Header{Key: h.Key, Value: []byte(h.Value)}
	}
	return out
}

// startPublish opens "<topic> publish", a producer span parented on the trace stored with the row
// (the request or subsystem fact that wrote it), and re-injects its context into headers so consumers
// continue from the publish. With telemetry off both the tracer and the propagator are no-ops.
func startPublish(ctx context.Context, m outbox.Message, headers *[]kafkago.Header) trace.Span {
	prop := otel.GetTextMapPropagator()
	carrier := headerCarrier{headers: headers}
	parent := prop.Extract(ctx, carrier)
	sctx, span := otel.Tracer(tracerName).Start(parent, m.Topic+" publish", trace.WithSpanKind(trace.SpanKindProducer),
		trace.WithAttributes(semconv.MessagingSystemKafka, semconv.MessagingDestinationName(m.Topic),
			semconv.MessagingOperationName("publish"), semconv.MessagingMessageID(m.EventID),
			attribute.String("cloudevents.event_type", m.EventType)))
	prop.Inject(sctx, carrier)
	return span
}
