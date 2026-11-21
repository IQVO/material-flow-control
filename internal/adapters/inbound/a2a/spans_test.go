package a2a

import (
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

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

// carry (Task 20 review, MCP parity): every skill run is one "a2a.skill <id>" span carrying the GenAI operation
// and the authority L1; a problem answer records the error and marks the span Error.
func TestEverySkillRunIsOneSpanWithTheAuthority(t *testing.T) {
	sr := recordSpans(t)
	client := agent(t, fakeReads{})
	send(t, client, skill("station_queue", map[string]any{"stationId": "ST1"}))
	send(t, client, skill("station_queue", map[string]any{"stationId": "ST9"}))
	var skills []sdktrace.ReadOnlySpan
	for _, s := range sr.Ended() {
		if s.Name() == "a2a.skill station_queue" {
			skills = append(skills, s)
		}
	}
	if len(skills) != 2 {
		t.Fatalf("skill spans = %d, want 2", len(skills))
	}
	for _, s := range skills {
		a := spanAttrs(s)
		if a["gen_ai.operation.name"] != "execute_tool" || a["gen_ai.tool.name"] != "station_queue" || a["a2a.skill"] != "station_queue" ||
			a["warehouse.authority"] != "L1" {
			t.Errorf("attributes = %v", a)
		}
	}
	if skills[0].Status().Code != codes.Unset || len(skills[0].Events()) != 0 {
		t.Errorf("the answered run = %+v %v", skills[0].Status(), skills[0].Events())
	}
	if skills[1].Status().Code != codes.Error || skills[1].Status().Description != "station-not-found: station not found" ||
		len(skills[1].Events()) != 1 || skills[1].Events()[0].Name != "exception" {
		t.Errorf("the problem run = %+v %v", skills[1].Status(), skills[1].Events())
	}
}
