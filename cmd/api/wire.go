package main

import (
	"context"
	"fmt"
	"log/slog"
	nethttp "net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	mfchttp "github.com/IQVO/material-flow-control/internal/adapters/inbound/http"
	"github.com/IQVO/material-flow-control/internal/adapters/inbound/kafka"
	"github.com/IQVO/material-flow-control/internal/adapters/outbound/amr"
	"github.com/IQVO/material-flow-control/internal/adapters/outbound/asrs"
	"github.com/IQVO/material-flow-control/internal/adapters/outbound/events"
	outkafka "github.com/IQVO/material-flow-control/internal/adapters/outbound/kafka"
	"github.com/IQVO/material-flow-control/internal/adapters/outbound/outbox"
	"github.com/IQVO/material-flow-control/internal/adapters/outbound/postgres"
	"github.com/IQVO/material-flow-control/internal/adapters/outbound/system"
	"github.com/IQVO/material-flow-control/internal/adapters/outbound/telemetry"
	"github.com/IQVO/material-flow-control/internal/application/usecases"
	layoutconfig "github.com/IQVO/material-flow-control/internal/config"
)

// wire builds the process: every adapter is real (Postgres, Kafka, the two subsystems over HTTP).
func wire(ctx context.Context, cfg config, log *slog.Logger) (*app, error) {
	l, err := layoutconfig.LoadLayout(cfg.LayoutFile)
	if err != nil {
		return nil, fmt.Errorf("layout %s: %w", cfg.LayoutFile, err)
	}
	if err := postgres.RunMigrations(cfg.MigrationsURL); err != nil {
		return nil, fmt.Errorf("migrations: %w", err)
	}
	pool, err := postgres.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	metrics, err := telemetry.NewTransportMetrics()
	if err != nil {
		pool.Close()
		return nil, err
	}
	outboxRepo := postgres.NewOutboxRepo(pool)
	observeOutbox(log, metrics, outboxRepo.Backlog)
	uow := postgres.NewUnitOfWork(pool)
	k := newKicker()
	svc, err := usecases.NewService(l, cfg.UseCases, usecases.Deps{UoW: uow, Lock: postgres.NewFlowLock(pool),
		Requests: postgres.NewRequests(pool), Orders: postgres.NewOrders(pool), Tracks: postgres.NewTracks(pool),
		Stations: postgres.NewStations(pool), Capacities: postgres.NewCapacities(pool), Units: postgres.NewUnits(pool),
		Commands: postgres.NewCommands(pool), Inbox: postgres.NewProcessedEvents(pool),
		Sink: events.NewSink(outboxRepo, system.IDs{}.NewID), Asrs: asrs.New(cfg.AsrsURL, cfg.SubsystemTimeout),
		Amr: amr.New(cfg.AmrURL, cfg.SubsystemTimeout), Clock: system.Clock{}, IDs: system.IDs{}, Metrics: metrics, Kick: k, Log: log})
	if err != nil {
		pool.Close()
		return nil, err
	}
	// ruling P4-R20: nothing is decided and /readyz is not ready before both consumers caught up with the backlog.
	catchUp := kafka.NewCatchUp(cfg.Brokers, log, kafka.Subscription{Topic: kafka.AsrsTopic, Group: cfg.AsrsGroup},
		kafka.Subscription{Topic: kafka.AmrTopic, Group: cfg.AmrGroup})
	readiness := mfchttp.NewReadiness(2*time.Second, time.Second,
		mfchttp.Dependency{Name: "postgres", Check: func(ctx context.Context) error { return pool.Ping(ctx) }},
		mfchttp.Dependency{Name: "kafka", Check: kafkaCheck(cfg.Brokers)},
		mfchttp.Dependency{Name: "subsystem-facts", Check: func(context.Context) error { return catchUp.Err() }})
	idem := mfchttp.NewIdempotency(postgres.NewIdempotencyStore(pool), uow, system.Clock{})
	sink := outkafka.NewRelaySink(cfg.Brokers)
	relay := outbox.NewOutboxRelay(outboxRepo, sink, log, outbox.WithInterval(cfg.OutboxInterval),
		outbox.WithBatchSize(cfg.OutboxBatch), outbox.WithMaxAttempts(cfg.OutboxMaxAttempts))
	a := &app{cfg: cfg, log: log, pool: pool, svc: svc, kick: k, readiness: readiness,
		server: &nethttp.Server{Handler: mfchttp.NewRouter(&mfchttp.Server{Service: svc, Idempotency: idem.Middleware, Readiness: readiness,
			ServiceName: cfg.ServiceName, CaughtUp: catchUp.Err}, log), ReadHeaderTimeout: 5 * time.Second},
		caughtUp:  catchUp.Err,
		consumers: stoppables(newConsumers(cfg, svc, log)),
		closers:   []func(){func() { _ = sink.Close() }, pool.Close},
		purgers: append([]purger{{"idempotency_keys", postgres.NewIdempotencyStore(pool), mfchttp.IdempotencyWindow},
			{"processed_events", postgres.NewProcessedEvents(pool), inboxRetention}}, retentionPurgers(pool, cfg)...),
		purged: metrics}
	a.loops = []func(context.Context){catchUp.Run, a.dispatch, a.tick, a.purge, func(ctx context.Context) { _ = relay.Run(ctx) }}
	return a, nil
}

// newConsumers builds the two fact consumers, asrs then amr, under the groups the environment names
// (MFC_ASRS_CONSUMER_GROUP, MFC_AMR_CONSUMER_GROUP).
func newConsumers(cfg config, svc *usecases.Service, log *slog.Logger) []*kafka.Consumer {
	return []*kafka.Consumer{kafka.NewAsrsConsumer(cfg.Brokers, cfg.AsrsGroup, svc, log),
		kafka.NewAmrConsumer(cfg.Brokers, cfg.AmrGroup, svc, log)}
}

func stoppables(cs []*kafka.Consumer) []stoppable {
	out := make([]stoppable, 0, len(cs))
	for _, c := range cs {
		out = append(out, stoppable{name: c.Name, run: c.Run, close: c.Close})
	}
	return out
}

// purger is a store with an age-based purge and how long its rows are kept (the 24-hour idempotency window; the
// 30-day inbox).
type purger struct {
	name  string
	store interface {
		Purge(ctx context.Context, before time.Time) (int64, error)
	}
	keep time.Duration
}

// retentionPurgers are the retention policies of ADR 0008 (issue #3), orders first: an order's purge frees its
// commands and moves for the policies after it. The moves policy keeps what a track view shows (HistoryLimit).
func retentionPurgers(pool *pgxpool.Pool, cfg config) []purger {
	r := cfg.Retention
	out := []purger{}
	for _, p := range []struct {
		store *postgres.Retention
		keep  time.Duration
	}{{postgres.NewOrderRetention(pool, r.Batch), r.Orders}, {postgres.NewRejectedRequestRetention(pool, r.Batch), r.Orders},
		{postgres.NewContainerMoveRetention(pool, r.Batch, cfg.UseCases.HistoryLimit), r.Moves},
		{postgres.NewCommandRetention(pool, r.Batch), r.Commands}, {postgres.NewOutboxRetention(pool, r.Batch), r.Outbox}} {
		out = append(out, purger{p.store.Table(), p.store, p.keep})
	}
	return out
}
