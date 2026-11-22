package usecases

import (
	"context"
	"errors"
	"fmt"

	"github.com/IQVO/material-flow-control/internal/application/facts"
	"github.com/IQVO/material-flow-control/internal/application/ports"
	"github.com/IQVO/material-flow-control/internal/domain/capacity"
	"github.com/IQVO/material-flow-control/internal/domain/layout"
	"github.com/IQVO/material-flow-control/internal/domain/stationqueue"
	"github.com/IQVO/material-flow-control/internal/domain/transportorder"
)

// ReadDeps are the read repositories of the read-only binaries (cmd/mcp, cmd/a2a, ADR 0007).
type ReadDeps struct {
	Requests   ports.TransportRequests
	Orders     ports.TransportOrders
	Tracks     ports.ContainerTracks
	Stations   ports.StationQueues
	Capacities ports.Capacities
	Units      ports.EquipmentUnits
	Clock      ports.Clock
}

// Reader is the read side only: it has no write method, and the Service inside it has no unit of work, lock,
// sink, command outbox or subsystem client, so no write path could run even if one were reached.
type Reader struct{ s *Service }

// NewReader wires the queries over read repositories.
func NewReader(l *layout.Layout, cfg Config, d ReadDeps) (*Reader, error) {
	if l == nil {
		return nil, errors.New("usecases: a layout is required")
	}
	for name, v := range map[string]any{"Requests": d.Requests, "Orders": d.Orders, "Tracks": d.Tracks, "Stations": d.Stations,
		"Capacities": d.Capacities, "Units": d.Units, "Clock": d.Clock} {
		if v == nil {
			return nil, fmt.Errorf("usecases: read dependency %s is required", name)
		}
	}
	return &Reader{s: &Service{l: l, cfg: cfg, d: Deps{Requests: d.Requests, Orders: d.Orders, Tracks: d.Tracks,
		Stations: d.Stations, Capacities: d.Capacities, Units: d.Units, Clock: d.Clock}}}, nil
}

func (r *Reader) Transport(ctx context.Context, requestID string) (TransportView, error) {
	return r.s.Transport(ctx, requestID)
}

func (r *Reader) OrderDetail(ctx context.Context, requestID string) (transportorder.Snapshot, error) {
	return r.s.OrderDetail(ctx, requestID)
}

func (r *Reader) Track(ctx context.Context, containerID string) (TrackView, error) {
	return r.s.Track(ctx, containerID)
}

func (r *Reader) Station(ctx context.Context, stationID string) (stationqueue.Snapshot, error) {
	return r.s.Station(ctx, stationID)
}

func (r *Reader) Subsystems(ctx context.Context) ([]capacity.Snapshot, error) {
	return r.s.Subsystems(ctx)
}

func (r *Reader) EquipmentUnits(ctx context.Context, asrsID string) ([]facts.EquipmentState, error) {
	return r.s.EquipmentUnits(ctx, asrsID)
}

func (r *Reader) Layout() *layout.Layout { return r.s.l }
