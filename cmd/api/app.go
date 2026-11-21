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
	"github.com/IQVO/material-flow-control/internal/adapters/inbound/kafka"
	outkafka "github.com/IQVO/material-flow-control/internal/adapters/outbound/kafka"
	"github.com/IQVO/material-flow-control/internal/adapters/outbound/outbox"
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
	relay     *outbox.Relay
	relaySink *outkafka.RelaySink
	consumers []*kafka.Consumer
	purgers   []purger
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
	var wg sync.WaitGroup
	loop := func(f func(context.Context)) {
		wg.Add(1)
		go func() { defer wg.Done(); f(ctx) }()
	}
	for _, c := range a.consumers {
		loop(func(ctx context.Context) {
			if err := c.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
				a.log.ErrorContext(ctx, "consumer stopped", "consumer", c.Name, "error", err)
			}
		})
	}
	loop(a.dispatch)
	loop(a.tick)
	loop(a.purge)
	loop(func(ctx context.Context) { _ = a.relay.Run(ctx) })
	serveErr := make(chan error, 1)
	go func() { serveErr <- a.server.Serve(ln) }()
	var err error
	select {
	case <-ctx.Done():
	case err = <-serveErr:
	}
	a.readiness.SetNotReady()
	drain, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	shutdownErr := a.server.Shutdown(drain)
	wg.Wait()
	for _, c := range a.consumers {
		_ = c.Close()
	}
	_ = a.relaySink.Close()
	a.pool.Close()
	if err != nil && !errors.Is(err, nethttp.ErrServerClosed) {
		return err
	}
	return shutdownErr
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
func (a *app) tick(ctx context.Context) {
	t := time.NewTicker(a.cfg.TickInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := a.svc.Tick(ctx); err != nil {
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
