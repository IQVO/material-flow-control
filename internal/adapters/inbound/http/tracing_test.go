package http_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	ahttp "github.com/IQVO/material-flow-control/internal/adapters/inbound/http"
)

// The fleet middleware order (ADR 0004): otelchi opens a server span named after the chi route
// pattern (one name per route, never per id), and the access log is written inside it.
func TestEveryRequestIsAServerSpanNamedAfterItsRoutePatternAndIsLogged(t *testing.T) {
	prev := otel.GetTracerProvider()
	rec := tracetest.NewSpanRecorder()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)))
	t.Cleanup(func() { otel.SetTracerProvider(prev) })
	var logs bytes.Buffer
	h := ahttp.NewRouter(&ahttp.Server{Service: newFixture(t).svc, ServiceName: "material-flow-control"}, slog.New(slog.NewJSONHandler(&logs, nil)))

	resp := httptest.NewRecorder()
	h.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/transport-requests/nope", nil))
	if resp.Code != http.StatusNotFound {
		t.Fatalf("status = %d", resp.Code)
	}
	spans := rec.Ended()
	if len(spans) != 1 || spans[0].SpanKind() != trace.SpanKindServer || !strings.Contains(spans[0].Name(), "/transport-requests/{transportRequestId}") {
		names := []string{}
		for _, s := range spans {
			names = append(names, s.Name())
		}
		t.Fatalf("server spans = %v, want one named after the route pattern", names)
	}
	var line map[string]any
	if err := json.Unmarshal(logs.Bytes(), &line); err != nil {
		t.Fatalf("access log is not one JSON line: %q (%v)", logs.String(), err)
	}
	if line["msg"] != "http request" || line["path"] != "/transport-requests/nope" || line["status"] != float64(404) {
		t.Fatalf("access log = %v", line)
	}
}
