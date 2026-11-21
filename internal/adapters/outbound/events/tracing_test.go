package events_test

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/IQVO/material-flow-control/internal/adapters/outbound/events"
	"github.com/IQVO/material-flow-control/internal/domain/capacity"
)

func traceHeader(t *testing.T, ctx context.Context) []string {
	t.Helper()
	rec := &rows{}
	if err := events.NewSink(rec, func() string { return "id" }).Append(ctx, capacity.SubsystemCapacityChanged{Snapshot: capacity.Station("ST1", 2, 1, 0, t0)}); err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, m := range rec.msgs {
		for _, h := range m.Headers {
			if h.Key == "traceparent" {
				out = append(out, h.Value)
			}
		}
	}
	return out
}

// The outbox row carries the W3C trace context of the unit of work that wrote it, so the relay's
// publish span (and every consumer) continues the request's or the subsystem fact's trace (ADR 0007).
func TestTheOutboxRowsCarryTheTraceContextOfTheirUnitOfWork(t *testing.T) {
	prev := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() { otel.SetTextMapPropagator(prev) })
	tid, _ := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	sid, _ := trace.SpanIDFromHex("00f067aa0ba902b7")
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{TraceID: tid, SpanID: sid, TraceFlags: trace.FlagsSampled}))
	got := traceHeader(t, ctx)
	want := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	if len(got) != 2 || got[0] != want || got[1] != want {
		t.Fatalf("traceparent = %v, want %s on both rows (events + analytics)", got, want)
	}
	if got := traceHeader(t, context.Background()); len(got) != 0 {
		t.Fatalf("a unit of work without a span stored traceparent %v", got)
	}
}
