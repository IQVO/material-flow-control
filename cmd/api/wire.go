package main

import (
	"context"
	"fmt"
	"log/slog"
	nethttp "net/http"
	"time"

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
	readiness := mfchttp.NewReadiness(2*time.Second, time.Second,
		mfchttp.Dependency{Name: "postgres", Check: func(ctx context.Context) error { return pool.Ping(ctx) }},
		mfchttp.Dependency{Name: "kafka", Check: kafkaCheck(cfg.Brokers)})
	idem := mfchttp.NewIdempotency(postgres.NewIdempotencyStore(pool), uow, system.Clock{})
	sink := outkafka.NewRelaySink(cfg.Brokers)
	return &app{cfg: cfg, log: log, pool: pool, svc: svc, kick: k, readiness: readiness, relaySink: sink,
		server: &nethttp.Server{Handler: mfchttp.NewRouter(&mfchttp.Server{Service: svc, Idempotency: idem.Middleware, Readiness: readiness,
			ServiceName: cfg.ServiceName}, log), ReadHeaderTimeout: 5 * time.Second},
		relay: outbox.NewOutboxRelay(outboxRepo, sink, log, outbox.WithInterval(cfg.OutboxInterval),
			outbox.WithBatchSize(cfg.OutboxBatch), outbox.WithMaxAttempts(cfg.OutboxMaxAttempts)),
		consumers: []*kafka.Consumer{kafka.NewAsrsConsumer(cfg.Brokers, cfg.AsrsGroup, svc, log),
			kafka.NewAmrConsumer(cfg.Brokers, cfg.AmrGroup, svc, log)},
		purgers: []purger{{"idempotency_keys", postgres.NewIdempotencyStore(pool), mfchttp.IdempotencyWindow},
			{"processed_events", postgres.NewProcessedEvents(pool), inboxRetention}}}, nil
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
