package subsystemhttp_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/IQVO/material-flow-control/internal/adapters/outbound/subsystemhttp"
	"github.com/IQVO/material-flow-control/internal/application/subsystem"
)

type seen struct {
	method, path, key, contentType, traceparent, body string
}

func server(t *testing.T, status int, contentType, body string) (*httptest.Server, *seen) {
	t.Helper()
	s := &seen{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		*s = seen{r.Method, r.URL.EscapedPath(), r.Header.Get("Idempotency-Key"), r.Header.Get("Content-Type"), r.Header.Get("traceparent"), string(b)}
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, s
}

func traced() context.Context {
	otel.SetTextMapPropagator(propagation.TraceContext{})
	sc := trace.NewSpanContext(trace.SpanContextConfig{TraceID: trace.TraceID{1}, SpanID: trace.SpanID{2}, TraceFlags: trace.FlagsSampled})
	return trace.ContextWithRemoteSpanContext(context.Background(), sc)
}

func TestAPostCarriesTheKeyTheBodyAndTheTraceAndDecodesTheAnswer(t *testing.T) {
	srv, s := server(t, http.StatusAccepted, "application/json", `{"id":"x-1"}`)
	c := subsystemhttp.New(srv.URL+"/", time.Second, nil)
	var out struct {
		ID string `json:"id"`
	}
	err := c.Do(traced(), http.MethodPost, subsystemhttp.Path("asrs", "AS RS", "jobs"), "k-1", map[string]string{"a": "b"}, &out)
	if err != nil || out.ID != "x-1" {
		t.Fatalf("Do = %v, out %+v", err, out)
	}
	if s.method != "POST" || s.path != "/asrs/AS%20RS/jobs" || s.key != "k-1" || s.contentType != "application/json" || s.body != `{"a":"b"}` ||
		s.traceparent != "00-01000000000000000000000000000000-0200000000000000-01" {
		t.Fatalf("request = %+v", s)
	}
}

func TestAGetSendsNoKeyAndNoBody(t *testing.T) {
	srv, s := server(t, http.StatusOK, "application/json", `{}`)
	if err := subsystemhttp.New(srv.URL, time.Second, nil).Do(context.Background(), http.MethodGet, "/x", "", nil, nil); err != nil {
		t.Fatal(err)
	}
	if s.key != "" || s.contentType != "" || s.body != "" {
		t.Fatalf("request = %+v", s)
	}
}

func TestProblemsAreRefusalsClassifiedByCode(t *testing.T) {
	cases := []struct {
		status    int
		body      string
		code      string
		retryable bool
	}{
		{422, `{"type":"about:blank","title":"t","status":422,"code":"storage-full","detail":"aisle 1 is full"}`, "storage-full", true},
		{404, `{"status":404,"code":"asrs-not-found"}`, "asrs-not-found", false},
		{409, `not json`, "http-409", false},
	}
	for _, tc := range cases {
		srv, _ := server(t, tc.status, "application/problem+json", tc.body)
		err := subsystemhttp.New(srv.URL, time.Second, []string{"storage-full"}).Do(context.Background(), http.MethodPost, "/x", "k", struct{}{}, nil)
		var r *subsystem.Refusal
		if !errors.As(err, &r) || r.Status != tc.status || r.Code != tc.code || r.Retryable != tc.retryable {
			t.Fatalf("%d %s: err = %v", tc.status, tc.code, err)
		}
		if errors.Is(err, subsystem.ErrUnreachable) {
			t.Fatalf("a refusal is not unreachable: %v", err)
		}
	}
}

func TestFiveHundredsTooManyRequestsTimeoutsAndClosedPortsAreUnreachable(t *testing.T) {
	for _, status := range []int{500, 503, 429} {
		srv, _ := server(t, status, "application/problem+json", `{"code":"internal-error"}`)
		if err := subsystemhttp.New(srv.URL, time.Second, nil).Do(context.Background(), http.MethodPost, "/x", "k", nil, nil); !errors.Is(err, subsystem.ErrUnreachable) {
			t.Fatalf("%d = %v", status, err)
		}
	}
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { time.Sleep(200 * time.Millisecond) }))
	defer slow.Close()
	if err := subsystemhttp.New(slow.URL, 20*time.Millisecond, nil).Do(context.Background(), http.MethodGet, "/x", "", nil, nil); !errors.Is(err, subsystem.ErrUnreachable) {
		t.Fatalf("timeout = %v", err)
	}
	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()
	err := subsystemhttp.New(closed.URL, time.Second, nil).Do(context.Background(), http.MethodGet, "/x", "", nil, nil)
	if !errors.Is(err, subsystem.ErrUnreachable) || !strings.Contains(err.Error(), "GET /x") {
		t.Fatalf("closed = %v", err)
	}
	srv, _ := server(t, http.StatusOK, "application/json", `{`)
	var out map[string]any
	if err := subsystemhttp.New(srv.URL, time.Second, nil).Do(context.Background(), http.MethodGet, "/x", "", nil, &out); !errors.Is(err, subsystem.ErrUnreachable) {
		t.Fatalf("undecodable 2xx = %v", err)
	}
}
