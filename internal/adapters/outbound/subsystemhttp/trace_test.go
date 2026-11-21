package subsystemhttp_test

import (
	"context"
	"net/http"
	"regexp"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/IQVO/material-flow-control/internal/adapters/outbound/subsystemhttp"
)

var traceparentRE = regexp.MustCompile(`^00-[0-9a-f]{32}-[0-9a-f]{16}-01$`)

// carry (Task 14 review): the dispatcher calls the subsystems from a background loop whose ctx
// carries no span, so the client starts a span of its own per call (otelhttp transport): with the
// SDK tracer provider and the W3C propagator installed (telemetry.Setup), every call carries a
// traceparent.
func TestACallFromABackgroundLoopCarriesATraceparentOfItsOwn(t *testing.T) {
	prevTP, prevProp := otel.GetTracerProvider(), otel.GetTextMapPropagator()
	tp := sdktrace.NewTracerProvider()
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() {
		_ = tp.Shutdown(context.Background())
		otel.SetTracerProvider(prevTP)
		otel.SetTextMapPropagator(prevProp)
	})
	srv, s := server(t, http.StatusAccepted, "application/json", `{"id":"x-1"}`)
	c := subsystemhttp.New(srv.URL, time.Second, nil)
	if err := c.Do(context.Background(), http.MethodPost, subsystemhttp.Path("fleets", "AMR1", "legs"), "k-1", map[string]string{}, nil); err != nil {
		t.Fatal(err)
	}
	if !traceparentRE.MatchString(s.traceparent) {
		t.Fatalf("traceparent = %q, want a sampled W3C traceparent", s.traceparent)
	}
}
