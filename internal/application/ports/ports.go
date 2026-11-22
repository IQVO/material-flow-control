// Package ports are the application's outbound ports: repositories, the unit of work and the flow lock, the
// clock, ids, the event sink (the outbox writer), the command outbox, the inbox, metrics and the dispatcher kick.
package ports

import (
	"context"
	"errors"
	"time"

	"github.com/IQVO/material-flow-control/internal/application/command"
	"github.com/IQVO/material-flow-control/internal/application/facts"
	"github.com/IQVO/material-flow-control/internal/domain/capacity"
	"github.com/IQVO/material-flow-control/internal/domain/containertrack"
	"github.com/IQVO/material-flow-control/internal/domain/stationqueue"
	"github.com/IQVO/material-flow-control/internal/domain/transportorder"
	"github.com/IQVO/material-flow-control/internal/domain/transportrequest"
)

var (
	// ErrNotFound is answered by every repository for an unknown key.
	ErrNotFound = errors.New("not found")
	// ErrDuplicate is answered by an Insert whose key exists.
	ErrDuplicate = errors.New("already exists")
)

type Clock interface{ Now() time.Time }

type IDs interface{ NewID() string }

// UnitOfWork runs fn atomically; Do joins an outer unit of work carried by ctx (the Idempotency-Key middleware
// opens one around every POST). AfterCommit runs fn after the OUTERMOST unit of work committed; outside one it
// runs at once; a rollback drops it.
type UnitOfWork interface {
	Do(ctx context.Context, fn func(ctx context.Context) error) error
	AfterCommit(ctx context.Context, fn func())
}

// FlowLock serializes every write unit of work (decision D19): Lock blocks until the caller's transaction holds it.
type FlowLock interface {
	Lock(ctx context.Context) error
}

type TransportRequests interface {
	Get(ctx context.Context, id string) (transportrequest.Record, error)
	Insert(ctx context.Context, r transportrequest.Record) error
}

// TransportOrders persists orders; ByLegRef finds the order owning a leg reference; Open lists the orders in
// progress in acceptance order.
type TransportOrders interface {
	Get(ctx context.Context, id string) (*transportorder.Order, error)
	ByLegRef(ctx context.Context, ref string) (*transportorder.Order, error)
	Open(ctx context.Context) ([]*transportorder.Order, error)
	Insert(ctx context.Context, o *transportorder.Order) error
	Save(ctx context.Context, o *transportorder.Order) error
}

// ContainerTracks persists tracks; Save also appends the track's pulled moves to its history; History is newest first.
type ContainerTracks interface {
	Get(ctx context.Context, containerID string) (*containertrack.Track, error)
	Save(ctx context.Context, t *containertrack.Track) error
	History(ctx context.Context, containerID string, limit int) ([]containertrack.Move, error)
}

type StationQueues interface {
	Get(ctx context.Context, stationID string) (*stationqueue.Queue, error)
	Save(ctx context.Context, q *stationqueue.Queue) error
}

// Capacities keeps the last snapshot per subsystem and station.
type Capacities interface {
	Get(ctx context.Context, kind capacity.Kind, id string) (capacity.Snapshot, bool, error)
	Save(ctx context.Context, s capacity.Snapshot) error
	List(ctx context.Context) ([]capacity.Snapshot, error)
}

// EquipmentUnits keeps the last state of every AS/RS unit (read views only).
type EquipmentUnits interface {
	Save(ctx context.Context, u facts.EquipmentState) error
	List(ctx context.Context, asrsID string) ([]facts.EquipmentState, error)
}

// Commands is the command outbox. Enqueue assigns Seq; Due lists PENDING commands with NextAttemptAt <= now in
// Seq order, at most limit.
type Commands interface {
	Enqueue(ctx context.Context, c command.Command) error
	Due(ctx context.Context, now time.Time, limit int) ([]command.Command, error)
	Save(ctx context.Context, c command.Command) error
}

// ProcessedEvents is the consumers' inbox: Claim records a CloudEvents id in the caller's unit of work and
// reports whether it was new.
type ProcessedEvents interface {
	Claim(ctx context.Context, eventID, eventType string, at time.Time) (bool, error)
}

// EventSink writes domain events to the transactional outbox, in the caller's unit of work.
type EventSink interface {
	Append(ctx context.Context, events ...any) error
}

// TransportMetrics is the Tier 2 port: every accepted (non-internal) transport that ended, called after commit.
// outcome is "completed" or "failed"; reason the FailReason (empty when completed).
type TransportMetrics interface {
	TransportEnded(ctx context.Context, purpose, outcome, reason string, took time.Duration)
}

// Kicker wakes the dispatcher after a commit that enqueued commands.
type Kicker interface{ Kick() }
