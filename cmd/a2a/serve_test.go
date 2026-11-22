package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

// Shutdown order (Task 19 lesson): when the run context ends, serve drains the in-flight request BEFORE it
// returns, so run's deferred pool close (and then the telemetry flush) can never cut a read in half.
func TestServeDrainsTheInFlightRequestBeforeReturning(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		_, _ = io.WriteString(w, "done")
	}), ReadHeaderTimeout: time.Second}
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- serve(ctx, srv, ln) }()
	answered := make(chan string, 1)
	go func() {
		resp, err := http.Get("http://" + ln.Addr().String() + "/")
		if err != nil {
			answered <- "error: " + err.Error()
			return
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		answered <- string(b)
	}()
	<-started
	cancel()
	select {
	case err := <-served:
		t.Fatalf("serve returned (%v) while a request was in flight", err)
	case <-time.After(150 * time.Millisecond):
	}
	close(release)
	if got := <-answered; got != "done" {
		t.Fatalf("the in-flight request = %q, want it drained to completion", got)
	}
	if err := <-served; err != nil {
		t.Fatalf("serve after the drain = %v", err)
	}
}

func TestServeReturnsAListenerFailure(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_ = ln.Close()
	if err := serve(context.Background(), &http.Server{ReadHeaderTimeout: time.Second}, ln); err == nil {
		t.Fatal("serve on a closed listener returned nil")
	}
}
