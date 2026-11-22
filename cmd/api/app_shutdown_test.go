package main

import (
	"context"
	"log/slog"
	nethttp "net/http"
	"net/http/httptest"
	"testing"
	"time"

	mfchttp "github.com/IQVO/material-flow-control/internal/adapters/inbound/http"
)

// carry (Task 19 review): the loops (dispatch, tick, purge, relay) have returned before the relay sink and
// the pool close; kills "closers before loops.Wait" and "no loops.Wait".
func TestTheLoopsStopBeforeTheSinkAndThePoolClose(t *testing.T) {
	ev := &journal{}
	a := &app{log: slog.New(slog.DiscardHandler), readiness: mfchttp.NewReadiness(time.Second, time.Second),
		server: &nethttp.Server{Handler: nethttp.NotFoundHandler(), ReadHeaderTimeout: time.Second},
		loops: []func(context.Context){func(ctx context.Context) {
			<-ctx.Done()
			time.Sleep(100 * time.Millisecond) // a pass finishing after the cancel
			ev.add("loop stopped")
		}},
		closers: []func(){func() { ev.add("close sink") }, func() { ev.add("close pool") }}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.run(ctx, listen(t)) }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := ev.list(); len(got) != 3 || got[0] != "loop stopped" || got[1] != "close sink" || got[2] != "close pool" {
		t.Fatalf("events = %v, want [loop stopped close sink close pool]", got)
	}
}

// carry (Task 19 review): readiness reads not_ready from the moment the shutdown starts (the in-flight
// request sees it); kills "no SetNotReady".
func TestReadinessIsNotReadyWhileTheShutdownDrains(t *testing.T) {
	rd := mfchttp.NewReadiness(time.Second, time.Second)
	probe := mfchttp.NewRouter(&mfchttp.Server{Readiness: rd}, slog.New(slog.DiscardHandler))
	inFlight, status := make(chan struct{}), make(chan int, 1)
	closing := make(chan string, 1)
	handler := nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		close(inFlight)
		<-closing // the shutdown has started
		rec := httptest.NewRecorder()
		probe.ServeHTTP(rec, httptest.NewRequest(nethttp.MethodGet, "/readyz", nil))
		status <- rec.Code
		w.WriteHeader(nethttp.StatusOK)
	})
	a := &app{log: slog.New(slog.DiscardHandler), readiness: rd,
		server:    &nethttp.Server{Handler: handler, ReadHeaderTimeout: time.Second},
		consumers: []stoppable{fakeConsumer("asrs", &journal{}, closing)}}
	ln := listen(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.run(ctx, ln) }()
	go func() {
		if resp, err := nethttp.Get("http://" + ln.Addr().String() + "/"); err == nil {
			_ = resp.Body.Close()
		}
	}()
	<-inFlight
	cancel()
	if code := <-status; code != nethttp.StatusServiceUnavailable {
		t.Fatalf("/readyz during the drain = %d, want 503", code)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
