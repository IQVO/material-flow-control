package subsystemhttp_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/IQVO/material-flow-control/internal/adapters/outbound/subsystemhttp"
	"github.com/IQVO/material-flow-control/internal/application/subsystem"
)

// carry (Task 14 review): a 3xx is never followed. Following it re-sent the POST as a bodiless GET with the
// Idempotency-Key to the Location and read its 200 as success; a misconfigured base URL must surface as
// ErrUnreachable instead.
func TestARedirectIsNeverFollowedAndIsUnreachable(t *testing.T) {
	var followed atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/redir", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/y", http.StatusFound) })
	mux.HandleFunc("/y", func(w http.ResponseWriter, _ *http.Request) { followed.Add(1); w.WriteHeader(http.StatusOK) })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	err := subsystemhttp.New(srv.URL, time.Second, nil).Do(context.Background(), http.MethodPost, "/redir", "k", struct{}{}, nil)
	if !errors.Is(err, subsystem.ErrUnreachable) || !strings.Contains(err.Error(), "POST /redir answered 302") {
		t.Fatalf("redirect = %v", err)
	}
	if n := followed.Load(); n != 0 {
		t.Fatalf("the redirect was followed %d time(s)", n)
	}
}

// carry (Task 14 review): 408 Request Timeout and 425 Too Early are transport answers like 429, not refusals
// classed FINAL by their missing code.
func TestRequestTimeoutAndTooEarlyAreUnreachableNotRefusals(t *testing.T) {
	for _, status := range []int{http.StatusRequestTimeout, http.StatusTooEarly} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) }))
		t.Cleanup(srv.Close)
		err := subsystemhttp.New(srv.URL, time.Second, nil).Do(context.Background(), http.MethodPost, "/x", "k", struct{}{}, nil)
		var r *subsystem.Refusal
		if !errors.Is(err, subsystem.ErrUnreachable) || errors.As(err, &r) {
			t.Fatalf("%d = %v", status, err)
		}
	}
}

// carry (Task 14 review): both producers build a problem type as problemBaseURI + code, so a problem that names
// only its type still classes by the slug; about:blank and an empty type keep the http-<status> fallback.
func TestAProblemWithOnlyATypeIsClassedByTheTypeSlug(t *testing.T) {
	cases := []struct {
		body, code string
		retryable  bool
	}{
		{`{"type":"https://errors.asrs-control.warehouse-systems.dev/storage-full","status":422}`, "storage-full", true},
		{`{"type":"about:blank","status":422}`, "http-422", false},
		{`{"type":"","status":422}`, "http-422", false},
	}
	for _, tc := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = io.WriteString(w, tc.body)
		}))
		t.Cleanup(srv.Close)
		err := subsystemhttp.New(srv.URL, time.Second, []string{"storage-full"}).Do(context.Background(), http.MethodPost, "/x", "k", struct{}{}, nil)
		var r *subsystem.Refusal
		if !errors.As(err, &r) || r.Code != tc.code || r.Retryable != tc.retryable {
			t.Fatalf("%s = %v", tc.body, err)
		}
	}
}
