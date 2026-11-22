package http_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	ahttp "github.com/IQVO/material-flow-control/internal/adapters/inbound/http"
)

func TestReadyzReportsDrainAndFailingDependencies(t *testing.T) {
	bad := true
	ready := ahttp.NewReadiness(time.Nanosecond, time.Second, ahttp.Dependency{Name: "mqtt", Check: func(context.Context) error {
		if bad {
			return errors.New("down")
		}
		return nil
	}})
	h := ahttp.NewRouter(&ahttp.Server{Readiness: ready}, nil)
	get := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
		return rec
	}
	if rec := get(); rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "dependency-unavailable") || !strings.Contains(rec.Body.String(), "mqtt") {
		t.Fatalf("failing dependency = %d %s", rec.Code, rec.Body.String())
	}
	bad = false
	if rec := get(); rec.Code != http.StatusOK {
		t.Fatalf("ready = %d", rec.Code)
	}
	ready.SetNotReady()
	if rec := get(); rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "not_ready") {
		t.Fatalf("draining = %d %s", rec.Code, rec.Body.String())
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("healthz = %d", rec.Code)
	}
}
