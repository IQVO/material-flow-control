package mcp

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// recordSpans installs a span recorder as the global tracer provider for one test.
func recordSpans(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	sr := tracetest.NewSpanRecorder()
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr)))
	t.Cleanup(func() { otel.SetTracerProvider(prev) })
	return sr
}

func spanAttrs(s sdktrace.ReadOnlySpan) map[attribute.Key]string {
	out := map[attribute.Key]string{}
	for _, kv := range s.Attributes() {
		out[kv.Key] = kv.Value.String()
	}
	return out
}

func spansNamed(sr *tracetest.SpanRecorder, name string) []sdktrace.ReadOnlySpan {
	var out []sdktrace.ReadOnlySpan
	for _, s := range sr.Ended() {
		if s.Name() == name {
			out = append(out, s)
		}
	}
	return out
}

// carry (Task 20 review): every tool call is one "mcp.tool <name>" span carrying the GenAI operation and the
// tool's authority; a failed call records the error and marks the span Error.
func TestEveryToolCallIsOneSpanWithTheAuthority(t *testing.T) {
	sr := recordSpans(t)
	cs := session(t, Deps{Reads: fakeReads{}})
	for _, id := range []string{"ST1", "ST9"} {
		if _, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "get_station", Arguments: map[string]any{"stationId": id}}); err != nil {
			t.Fatal(err)
		}
	}
	tools := spansNamed(sr, "mcp.tool get_station")
	if len(tools) != 2 {
		t.Fatalf("tool spans = %d, want 2", len(tools))
	}
	for _, s := range tools {
		a := spanAttrs(s)
		if a["gen_ai.operation.name"] != "execute_tool" || a["gen_ai.tool.name"] != "get_station" || a["warehouse.authority"] != "L1" {
			t.Errorf("attributes = %v", a)
		}
	}
	if tools[0].Status().Code != codes.Unset || len(tools[0].Events()) != 0 {
		t.Errorf("the successful call = %+v %v", tools[0].Status(), tools[0].Events())
	}
	if tools[1].Status().Code != codes.Error || tools[1].Status().Description != "station-not-found: station not found" ||
		len(tools[1].Events()) != 1 || tools[1].Events()[0].Name != "exception" {
		t.Errorf("the failed call = %+v %v", tools[1].Status(), tools[1].Events())
	}
}
