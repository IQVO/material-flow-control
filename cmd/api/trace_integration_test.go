//go:build integration

package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/IQVO/material-flow-control/internal/domain/transportrequest"
	"github.com/IQVO/material-flow-control/internal/testing/pgtest"
)

// carry (Task 14 review): a REQUEST_LEG the dispatcher sends from its background loop (no span
// in ctx) reaches amr-fleet-control with a traceparent once telemetry installed the tracer
// provider and the W3C propagator; wired by wire(), so the real composition is what is proven.
func TestADispatchedRequestLegReachesTheFleetWithATraceparent(t *testing.T) {
	prevTP, prevProp := otel.GetTracerProvider(), otel.GetTextMapPropagator()
	tp := sdktrace.NewTracerProvider()
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() {
		_ = tp.Shutdown(context.Background())
		otel.SetTracerProvider(prevTP)
		otel.SetTextMapPropagator(prevProp)
	})
	var mu sync.Mutex
	var seen []string
	fleet := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		mu.Lock()
		seen = append(seen, r.Method+" "+r.URL.Path+" traceparent="+r.Header.Get("traceparent"))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"leg_id":"L-1","status":"REQUESTED"}`)
	}))
	t.Cleanup(fleet.Close)
	url := pgtest.URL(t)
	cfg, err := loadConfig(env(map[string]string{"DATABASE_URL": url, "KAFKA_BROKERS": "127.0.0.1:1",
		"ASRS_CONTROL_URL": "http://127.0.0.1:1", "AMR_FLEET_CONTROL_URL": fleet.URL, "MFC_LAYOUT_FILE": "../../config/flow-layout.example.yaml"}))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	a, err := wire(ctx, cfg, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	// carry (Task 19 review): close what wire built, so nothing keeps dialling 127.0.0.1:1 for the rest
	// of the test binary.
	t.Cleanup(func() { closeWired(a) })
	if _, err := a.svc.RequestTransport(ctx, transportrequest.Request{ID: "rq-1", ContainerID: "TOTE-1", Source: "WH1-RCV-DEC1",
		Destination: "WH1-PCK-PK1", Purpose: transportrequest.PurposeOutbound, Priority: transportrequest.PriorityNormal,
		DueBy: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if n, err := a.svc.DispatchDue(context.Background()); err != nil || n == 0 {
		t.Fatalf("DispatchDue = %d, %v", n, err)
	}
	mu.Lock()
	defer mu.Unlock()
	re := regexp.MustCompile(`^POST /fleets/AMR1/legs traceparent=00-[0-9a-f]{32}-[0-9a-f]{16}-01$`)
	if len(seen) != 1 || !re.MatchString(seen[0]) {
		t.Fatalf("fleet saw %q, want one POST /fleets/AMR1/legs with a sampled traceparent", seen)
	}
}

// closeWired closes what wire built: the readers and DLQ writers, then the relay sink and the pool.
func closeWired(a *app) {
	for _, c := range a.consumers {
		_ = c.close()
	}
	for _, f := range a.closers {
		f()
	}
}
