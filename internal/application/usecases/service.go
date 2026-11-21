// Package usecases is material-flow-control's application layer: one Service whose every write runs in one unit
// of work under the flow lock, and whose subsystem calls are queued as commands (ADR 0002).
package usecases

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/IQVO/material-flow-control/internal/application/ports"
	"github.com/IQVO/material-flow-control/internal/domain/layout"
	"github.com/IQVO/material-flow-control/internal/domain/transportrequest"
)

var (
	ErrInvalidRequest     = errors.New("invalid request")
	ErrRequestConflict    = errors.New("the transport_request_id was used with another request")
	ErrRequestNotFound    = errors.New("transport request not found")
	ErrTrackNotFound      = errors.New("container track not found")
	ErrStationNotFound    = errors.New("station not found")
	ErrUnknownLocation    = errors.New("the location is not a place of this automation")
	ErrTransportEnded     = errors.New("the transport has ended")
	ErrNotCancellable     = errors.New("the transport cannot be cancelled now")
	ErrNotInClarification = errors.New("the container is not in clarification")
)

// RejectedError is a transport rejection. Its record and TransportRejected are committed (replay class OUTCOME).
type RejectedError struct {
	RequestID string
	Reason    transportrequest.Reason
	Detail    string
}

func (e *RejectedError) Error() string {
	return fmt.Sprintf("transport request %s rejected: %s (%s)", e.RequestID, e.Reason, e.Detail)
}

// Config holds the use-case tunables (cmd/api reads them from the environment).
type Config struct {
	LegRetryLimit      int
	LegRequestDeadline time.Duration
	LegTimeout         time.Duration
	RetryInitial       time.Duration
	RetryMax           time.Duration
	DispatchBatch      int
	HistoryLimit       int
}

// DefaultConfig is decisions D13, D14 and D16.
func DefaultConfig() Config {
	return Config{LegRetryLimit: 2, LegRequestDeadline: 300 * time.Second, LegTimeout: 900 * time.Second,
		RetryInitial: time.Second, RetryMax: 30 * time.Second, DispatchBatch: 16, HistoryLimit: 20}
}

// Deps are the Service's ports. Asrs, Amr, Metrics and Kick may be nil (the read-only binaries).
type Deps struct {
	UoW        ports.UnitOfWork
	Lock       ports.FlowLock
	Requests   ports.TransportRequests
	Orders     ports.TransportOrders
	Tracks     ports.ContainerTracks
	Stations   ports.StationQueues
	Capacities ports.Capacities
	Units      ports.EquipmentUnits
	Commands   ports.Commands
	Inbox      ports.ProcessedEvents
	Sink       ports.EventSink
	Asrs       ports.AsrsClient
	Amr        ports.AmrClient
	Clock      ports.Clock
	IDs        ports.IDs
	Metrics    ports.TransportMetrics
	Kick       ports.Kicker
	Log        *slog.Logger
}

// Service is the application.
type Service struct {
	l   *layout.Layout
	cfg Config
	d   Deps
}

// NewService checks the required dependencies.
func NewService(l *layout.Layout, cfg Config, d Deps) (*Service, error) {
	if l == nil {
		return nil, errors.New("usecases: a layout is required")
	}
	required := []struct {
		name string
		v    any
	}{{"UoW", d.UoW}, {"Lock", d.Lock}, {"Requests", d.Requests}, {"Orders", d.Orders}, {"Tracks", d.Tracks},
		{"Stations", d.Stations}, {"Capacities", d.Capacities}, {"Units", d.Units}, {"Commands", d.Commands},
		{"Inbox", d.Inbox}, {"Sink", d.Sink}, {"Clock", d.Clock}, {"IDs", d.IDs}}
	for _, r := range required {
		if r.v == nil {
			return nil, fmt.Errorf("usecases: dependency %s is required", r.name)
		}
	}
	if d.Log == nil {
		d.Log = slog.Default()
	}
	return &Service{l: l, cfg: cfg, d: d}, nil
}

// Layout is the loaded material-flow layout.
func (s *Service) Layout() *layout.Layout { return s.l }

func (s *Service) now() time.Time { return s.d.Clock.Now().UTC().Truncate(time.Microsecond) }

// write runs fn in one unit of work under the flow lock and kicks the dispatcher after commit.
func (s *Service) write(ctx context.Context, fn func(ctx context.Context) error) error {
	return s.d.UoW.Do(ctx, func(ctx context.Context) error {
		if err := s.d.Lock.Lock(ctx); err != nil {
			return err
		}
		if err := fn(ctx); err != nil {
			return err
		}
		if s.d.Kick != nil {
			s.d.UoW.AfterCommit(ctx, s.d.Kick.Kick)
		}
		return nil
	})
}

// consume applies one Kafka fact: the inbox claim and every effect in one unit of work (checklist item 3).
func (s *Service) consume(ctx context.Context, eventID, eventType string, fn func(ctx context.Context) error) error {
	return s.write(ctx, func(ctx context.Context) error {
		fresh, err := s.d.Inbox.Claim(ctx, eventID, eventType, s.now())
		if err != nil || !fresh {
			return err
		}
		return fn(ctx)
	})
}
