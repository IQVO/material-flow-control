package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	nethttp "net/http"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	kafkago "github.com/segmentio/kafka-go"

	mfchttp "github.com/IQVO/material-flow-control/internal/adapters/inbound/http"
	"github.com/IQVO/material-flow-control/internal/application/usecases"
)

// inboxRetention bounds processed_events: far beyond any redelivery of a committed offset.
const inboxRetention = 30 * 24 * time.Hour

type app struct {
	cfg       config
	log       *slog.Logger
	pool      *pgxpool.Pool
	svc       *usecases.Service
	kick      *kicker
	readiness *mfchttp.Readiness
	server    *nethttp.Server
	consumers []stoppable
	loops     []func(context.Context)
	closers   []func()
	purgers   []purger
	// caughtUp is nil once the subsystem facts published before boot are applied (ruling P4-R20); nil = no gate.
	caughtUp func() error
}

// stoppable is one fact consumer as run sees it: run until ctx ends, then close. carry (Task 15
// review): run closes each one as soon as its own run returned, concurrently with the other and
// with the HTTP drain.
type stoppable struct {
	name  string
	run   func(context.Context) error
	close func() error
}

// kicker wakes the dispatcher after a commit that queued a command (ports.Kicker); it never blocks.
type kicker struct{ ch chan struct{} }

func newKicker() *kicker { return &kicker{ch: make(chan struct{}, 1)} }

func (k *kicker) Kick() {
	select {
	case k.ch <- struct{}{}:
	default:
	}
}

func kafkaCheck(brokers []string) func(context.Context) error {
	return func(ctx context.Context) error {
		conn, err := (&kafkago.Dialer{Timeout: time.Second}).DialContext(ctx, "tcp", brokers[0])
		if err != nil {
			return err
		}
		return conn.Close()
	}
}

func (a *app) run(ctx context.Context, ln net.Listener) error {
	// A failed listener ends the process too: cancelling run's own context stops every loop and
	// consumer, as a signal would.
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	var loops sync.WaitGroup
	for _, f := range a.loops {
		loops.Go(func() { f(ctx) })
	}
	stopped := a.startConsumers(ctx)
	serveErr := make(chan error, 1)
	go func() { serveErr <- a.server.Serve(ln) }()
	var err error
	select {
	case <-ctx.Done():
	case err = <-serveErr:
	}
	a.readiness.SetNotReady()
	stop()
	closed := a.closeConsumers(stopped)
	drain, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	shutdownErr := a.server.Shutdown(drain)
	closed.Wait()
	loops.Wait()
	for _, f := range a.closers {
		f()
	}
	if err != nil && !errors.Is(err, nethttp.ErrServerClosed) {
		return err
	}
	return shutdownErr
}

// startConsumers runs every consumer; each returned channel closes when that consumer's run returned.
func (a *app) startConsumers(ctx context.Context) []chan struct{} {
	stopped := make([]chan struct{}, len(a.consumers))
	for i, c := range a.consumers {
		stopped[i] = make(chan struct{})
		go func() {
			defer close(stopped[i])
			if err := c.run(ctx); err != nil && !errors.Is(err, context.Canceled) {
				a.log.ErrorContext(ctx, "consumer stopped", "consumer", c.name, "error", err)
			}
		}()
	}
	return stopped
}

// closeConsumers closes each consumer once its run returned, all of them concurrently and while the
// caller drains HTTP (carry, Task 15 review); Wait on the result before closing the pool.
func (a *app) closeConsumers(stopped []chan struct{}) *sync.WaitGroup {
	var wg sync.WaitGroup
	for i, c := range a.consumers {
		wg.Go(func() {
			<-stopped[i]
			if err := c.close(); err != nil {
				a.log.Warn("consumer close failed", "consumer", c.name, "error", err)
			}
		})
	}
	return &wg
}

// dispatch runs DispatchDue until nothing is due, on every kick and every DispatchPoll (decision D19).
func (a *app) dispatch(ctx context.Context) {
	t := time.NewTicker(a.cfg.DispatchPoll)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-a.kick.ch:
		case <-t.C:
		}
		for ctx.Err() == nil {
			n, err := a.svc.DispatchDue(ctx)
			if err != nil {
				a.log.WarnContext(ctx, "dispatch failed", "error", err)
				break
			}
			if n == 0 {
				break
			}
		}
	}
}

// tick fails stale legs TIMEOUT (decision D16).
func (a *app) tick(ctx context.Context) { a.tickLoop(ctx, a.svc.Tick) }

// tickLoop runs tick every TickInterval, but never before the subsystem facts published before boot are applied: a
// leg whose progress sits in that backlog is not stale (ruling P4-R20).
func (a *app) tickLoop(ctx context.Context, tick func(context.Context) (int, error)) {
	t := time.NewTicker(a.cfg.TickInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if a.caughtUp != nil && a.caughtUp() != nil {
				continue
			}
			if _, err := tick(ctx); err != nil {
				a.log.WarnContext(ctx, "tick failed", "error", err)
			}
		}
	}
}

// purge drops idempotency answers older than the window and inbox rows older than inboxRetention, hourly.
func (a *app) purge(ctx context.Context) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		now := time.Now().UTC()
		for _, p := range a.purgers {
			if n, err := p.store.Purge(ctx, now.Add(-p.keep)); err != nil {
				a.log.WarnContext(ctx, "purge failed", "table", p.name, "error", err)
			} else if n > 0 {
				a.log.InfoContext(ctx, "purged", "table", p.name, "rows", n)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
