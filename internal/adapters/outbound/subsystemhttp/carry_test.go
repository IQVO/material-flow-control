package subsystemhttp_test

import (
	"context"
	"errors"
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
