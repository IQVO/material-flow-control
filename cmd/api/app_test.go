package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	nethttp "net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	mfchttp "github.com/IQVO/material-flow-control/internal/adapters/inbound/http"
)

// journal records what the shutdown did, in order.
type journal struct {
	mu  sync.Mutex
	got []string
}

func (e *journal) add(s string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.got = append(e.got, s)
}

func (e *journal) list() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.got...)
}

// fakeConsumer runs until ctx ends; its Close records itself and reports on closing.
func fakeConsumer(name string, ev *journal, closing chan<- string) stoppable {
	return stoppable{name: name,
		run:   func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() },
		close: func() error { ev.add("close " + name); closing <- name; return nil }}
}

func listen(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return ln
}

// carry (Task 15 review): the two consumers close concurrently with each other and while the HTTP
// drain still runs; the relay sink and the pool close last, after both and after the drain. The
// in-flight request answers only once BOTH consumers started closing: a sequential shutdown (drain,
// then consumers) would make it wait out its 3 s guard and fail.
func TestTheConsumersCloseConcurrentlyWhileTheHTTPDrainRuns(t *testing.T) {
	ev := &journal{}
	closing := make(chan string, 2)
	inFlight := make(chan struct{})
	var overlapped atomic.Bool
	handler := nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		close(inFlight)
		seen := map[string]bool{}
		guard := time.After(3 * time.Second)
		for len(seen) < 2 {
			select {
			case n := <-closing:
				seen[n] = true
			case <-guard:
				ev.add("drain gave up waiting for the consumers")
				w.WriteHeader(nethttp.StatusServiceUnavailable)
				return
			}
		}
		overlapped.Store(true)
		ev.add("request answered")
		w.WriteHeader(nethttp.StatusOK)
	})
	a := &app{log: slog.New(slog.DiscardHandler), readiness: mfchttp.NewReadiness(time.Second, time.Second),
		server:    &nethttp.Server{Handler: handler, ReadHeaderTimeout: time.Second},
		consumers: []stoppable{fakeConsumer("asrs", ev, closing), fakeConsumer("amr", ev, closing)},
		closers:   []func(){func() { ev.add("close sink") }, func() { ev.add("close pool") }}}
	ln := listen(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.run(ctx, ln) }()
	answered := make(chan int, 1)
	go func() {
		resp, err := nethttp.Get("http://" + ln.Addr().String() + "/")
		if err != nil {
			answered <- 0
			return
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		answered <- resp.StatusCode
	}()
	<-inFlight
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("run did not return; events %v", ev.list())
	}
	if code := <-answered; code != nethttp.StatusOK || !overlapped.Load() {
		t.Fatalf("in-flight request = %d, overlapped %v; events %v", code, overlapped.Load(), ev.list())
	}
	got := ev.list()
	if len(got) != 5 || got[3] != "close sink" || got[4] != "close pool" {
		t.Fatalf("events = %v, want both consumer closes and the answer, then close sink, close pool", got)
	}
}

// A consumer whose Run fails on its own (not a cancellation) is still closed once.
func TestAConsumerThatStoppedOnItsOwnIsClosedOnShutdown(t *testing.T) {
	ev := &journal{}
	stopped := make(chan struct{})
	a := &app{log: slog.New(slog.DiscardHandler), readiness: mfchttp.NewReadiness(time.Second, time.Second),
		server: &nethttp.Server{Handler: nethttp.NotFoundHandler(), ReadHeaderTimeout: time.Second},
		consumers: []stoppable{{name: "asrs", run: func(context.Context) error { close(stopped); return errors.New("broker gone") },
			close: func() error { ev.add("close asrs"); return errors.New("already closed") }}}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.run(ctx, listen(t)) }()
	<-stopped
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run did not return")
	}
	if got := ev.list(); len(got) != 1 || got[0] != "close asrs" {
		t.Fatalf("events = %v", got)
	}
}

// A listener that fails ends the process: run cancels its own context so every loop and consumer
// stops, and returns the serve error (the brief's run waited on loops that only a signal stopped).
func TestAServeErrorStopsEveryLoopAndIsReturned(t *testing.T) {
	ev := &journal{}
	closing := make(chan string, 1)
	a := &app{log: slog.New(slog.DiscardHandler), readiness: mfchttp.NewReadiness(time.Second, time.Second),
		server:    &nethttp.Server{Handler: nethttp.NotFoundHandler(), ReadHeaderTimeout: time.Second},
		consumers: []stoppable{fakeConsumer("asrs", ev, closing)},
		loops:     []func(context.Context){func(ctx context.Context) { <-ctx.Done(); ev.add("loop stopped") }}}
	ln := listen(t)
	_ = ln.Close()
	done := make(chan error, 1)
	go func() { done <- a.run(context.Background(), ln) }()
	select {
	case err := <-done:
		if err == nil || errors.Is(err, nethttp.ErrServerClosed) {
			t.Fatalf("run = %v, want the listener's error", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("run did not return after the listener failed; events %v", ev.list())
	}
	if got := ev.list(); len(got) != 2 {
		t.Fatalf("events = %v, want the consumer closed and the loop stopped", got)
	}
}
