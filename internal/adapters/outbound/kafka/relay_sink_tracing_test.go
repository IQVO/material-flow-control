package kafka_test

import (
	"context"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/IQVO/material-flow-control/internal/adapters/outbound/kafka"
	"github.com/IQVO/material-flow-control/internal/application/outbox"
)

// withTracing installs a recording tracer provider and the W3C propagator for one test.
func withTracing(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	prevTP, prevProp := otel.GetTracerProvider(), otel.GetTextMapPropagator()
	rec := tracetest.NewSpanRecorder()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)))
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() { otel.SetTracerProvider(prevTP); otel.SetTextMapPropagator(prevProp) })
	return rec
}

const storedParent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"

func header(m []outbox.Header, k string) string {
	for _, h := range m {
		if h.Key == k {
			return h.Value
		}
	}
	return ""
}

func TestEveryRelayedMessageIsAPublishSpanParentedOnTheStoredTraceAndReinjected(t *testing.T) {
	rec := withTracing(t)
	w := &fakeWriter{}
	s := kafka.NewRelaySinkWithWriter(w)
	msg := outbox.Message{EventID: "e1", Topic: "warehouse.material-flow-control.events", EventType: "com.warehouse.wes.material-flow-control.transportorder.TransportCompleted",
		Key: []byte("to-1"), Value: []byte("{}"), Headers: []outbox.Header{{Key: "content-type", Value: "x"}, {Key: "traceparent", Value: storedParent}}}
	if err := s.Send(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	spans := rec.Ended()
	if len(spans) != 1 || spans[0].Name() != "warehouse.material-flow-control.events publish" || spans[0].SpanKind() != trace.SpanKindProducer {
		t.Fatalf("spans = %d (%v), want one producer span named after the topic", len(spans), spans)
	}
	sp := spans[0]
	if sp.SpanContext().TraceID().String() != "4bf92f3577b34da6a3ce929d0e0e4736" || sp.Parent().SpanID().String() != "00f067aa0ba902b7" {
		t.Fatalf("publish span is not a child of the stored trace: trace %s parent %s", sp.SpanContext().TraceID(), sp.Parent().SpanID())
	}
	var got string
	n := 0
	for _, h := range w.got[0].Headers {
		if h.Key == "traceparent" {
			got, n = string(h.Value), n+1
		}
	}
	if n != 1 || !strings.Contains(got, sp.SpanContext().SpanID().String()) {
		t.Fatalf("traceparent on the wire = %q (x%d), want exactly one naming the publish span %s", got, n, sp.SpanContext().SpanID())
	}
	if string(w.got[0].Headers[0].Value) != "x" || header(msg.Headers, "traceparent") != storedParent {
		t.Fatal("the other headers must pass through and the stored message must not be modified")
	}
}

func TestARelayedMessageWithoutATraceStartsOne(t *testing.T) {
	rec := withTracing(t)
	w := &fakeWriter{}
	if err := kafka.NewRelaySinkWithWriter(w).Send(context.Background(), outbox.Message{Topic: "warehouse.material-flow-control.analytics", Value: []byte("{}")}); err != nil {
		t.Fatal(err)
	}
	if len(rec.Ended()) != 1 || rec.Ended()[0].Parent().IsValid() {
		t.Fatal("a message without a stored trace must start a root publish span")
	}
}
