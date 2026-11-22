package main

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"
)

// ruling P4-R20: the leg-timeout tick decides nothing while the subsystem facts published before boot are still being
// applied (a leg whose progress facts sit in the backlog would be failed TIMEOUT on stale state, D16), and ticks
// normally once the gate is open.
func TestTheTickFailsNoLegBeforeTheSubsystemFactsAreCaughtUp(t *testing.T) {
	var open atomic.Bool
	var ticks atomic.Int32
	a := &app{log: slog.New(slog.DiscardHandler), cfg: config{TickInterval: time.Millisecond},
		caughtUp: func() error {
			if open.Load() {
				return nil
			}
			return errors.New("catching up")
		}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		a.tickLoop(ctx, func(context.Context) (int, error) { ticks.Add(1); return 0, nil })
	}()
	time.Sleep(50 * time.Millisecond)
	if n := ticks.Load(); n != 0 {
		cancel()
		<-done
		t.Fatalf("Tick ran %d times while catching up, want 0", n)
	}
	open.Store(true)
	deadline := time.Now().Add(5 * time.Second)
	for ticks.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-done
	if ticks.Load() == 0 {
		t.Fatal("Tick never ran after the gate opened")
	}
}
