package usecases

import (
	"context"
	"errors"
	"time"

	"github.com/IQVO/material-flow-control/internal/application/facts"
	"github.com/IQVO/material-flow-control/internal/application/ports"
	"github.com/IQVO/material-flow-control/internal/domain/capacity"
	"github.com/IQVO/material-flow-control/internal/domain/containertrack"
	"github.com/IQVO/material-flow-control/internal/domain/stationqueue"
	"github.com/IQVO/material-flow-control/internal/domain/transportorder"
	"github.com/IQVO/material-flow-control/internal/domain/transportrequest"
)

// View statuses of a transport request (the relationship-1 TransportOrder schema).
const (
	ViewInProgress      = "IN_PROGRESS"
	ViewCancelRequested = "CANCEL_REQUESTED"
	ViewCompleted       = "COMPLETED"
	ViewFailed          = "FAILED"
	ViewRejected        = "REJECTED"
)

// TransportView is what the WES sees of one request: no equipment vocabulary.
type TransportView struct {
	RequestID         string
	OrderID           string
	ContainerID       string
	Source            string
	Destination       string
	Purpose           transportrequest.Purpose
	Priority          transportrequest.Priority
	DueBy             time.Time
	Status            string
	Reason            string
	ContainerLocation string
	LegsTotal         int
	LegsDone          int
	AcceptedAt        time.Time
	RejectedAt        time.Time
	EndedAt           time.Time
}

// TrackView is a container's track with its latest moves, newest first.
type TrackView struct {
	Track   containertrack.Snapshot
	History []containertrack.Move
}

func baseView(rec transportrequest.Record) TransportView {
	r := rec.Request
	return TransportView{RequestID: r.ID, ContainerID: r.ContainerID, Source: r.Source, Destination: r.Destination,
		Purpose: r.Purpose, Priority: r.Priority, DueBy: r.DueBy}
}

func rejectedView(rec transportrequest.Record) TransportView {
	v := baseView(rec)
	v.Status, v.Reason, v.RejectedAt = ViewRejected, string(rec.Reason), rec.At
	return v
}

func legCounts(legs []transportorder.Leg) (total, done int) {
	seqs, doneSeqs := map[int]bool{}, map[int]bool{}
	for _, l := range legs {
		seqs[l.Seq] = true
		if l.Status == transportorder.LegDone {
			doneSeqs[l.Seq] = true
		}
	}
	return len(seqs), len(doneSeqs)
}

func viewOf(rec transportrequest.Record, o transportorder.Snapshot, pos containertrack.Position) TransportView {
	v := baseView(rec)
	v.OrderID, v.AcceptedAt, v.EndedAt = o.ID, o.AcceptedAt, o.EndedAt
	v.LegsTotal, v.LegsDone = legCounts(o.Legs)
	switch o.Status {
	case transportorder.StatusCompleted:
		v.Status, v.ContainerLocation = ViewCompleted, o.Destination.Code
	case transportorder.StatusFailed:
		v.Status, v.Reason, v.ContainerLocation = ViewFailed, string(o.Reason), o.ContainerLocation
	default:
		v.Status, v.ContainerLocation = ViewInProgress, confirmedLocation(pos)
		if o.CancelRequested {
			v.Status = ViewCancelRequested
		}
	}
	return v
}

func (s *Service) position(ctx context.Context, containerID string) (containertrack.Position, error) {
	t, err := s.d.Tracks.Get(ctx, containerID)
	if errors.Is(err, ports.ErrNotFound) {
		return containertrack.Position{Kind: containertrack.Unknown}, nil
	}
	if err != nil {
		return containertrack.Position{}, err
	}
	return t.Position(), nil
}

func (s *Service) transportView(ctx context.Context, rec transportrequest.Record) (TransportView, error) {
	if rec.Outcome == transportrequest.OutcomeRejected {
		return rejectedView(rec), nil
	}
	o, err := s.d.Orders.Get(ctx, rec.OrderID)
	if err != nil {
		return TransportView{}, err
	}
	pos, err := s.position(ctx, rec.Request.ContainerID)
	if err != nil {
		return TransportView{}, err
	}
	return viewOf(rec, o.Snapshot(), pos), nil
}

func (s *Service) record(ctx context.Context, requestID string) (transportrequest.Record, error) {
	rec, err := s.d.Requests.Get(ctx, requestID)
	if errors.Is(err, ports.ErrNotFound) {
		return rec, ErrRequestNotFound
	}
	return rec, err
}

// Transport is the view of one transport request (operators and agents; never for polling).
func (s *Service) Transport(ctx context.Context, requestID string) (TransportView, error) {
	rec, err := s.record(ctx, requestID)
	if err != nil {
		return TransportView{}, err
	}
	return s.transportView(ctx, rec)
}

// OrderDetail is the WCS-internal view of an accepted request: legs, subsystem ids and robots (MCP and A2A).
func (s *Service) OrderDetail(ctx context.Context, requestID string) (transportorder.Snapshot, error) {
	rec, err := s.record(ctx, requestID)
	if err != nil {
		return transportorder.Snapshot{}, err
	}
	if rec.Outcome == transportrequest.OutcomeRejected {
		return transportorder.Snapshot{}, ErrTransportEnded
	}
	o, err := s.d.Orders.Get(ctx, rec.OrderID)
	if err != nil {
		return transportorder.Snapshot{}, err
	}
	return o.Snapshot(), nil
}

func (s *Service) trackView(ctx context.Context, t *containertrack.Track) (TrackView, error) {
	h, err := s.d.Tracks.History(ctx, t.ContainerID(), s.cfg.HistoryLimit)
	if err != nil {
		return TrackView{}, err
	}
	return TrackView{Track: t.Snapshot(), History: h}, nil
}

// Track is one container's track.
func (s *Service) Track(ctx context.Context, containerID string) (TrackView, error) {
	t, err := s.d.Tracks.Get(ctx, containerID)
	if errors.Is(err, ports.ErrNotFound) {
		return TrackView{}, ErrTrackNotFound
	}
	if err != nil {
		return TrackView{}, err
	}
	return s.trackView(ctx, t)
}

// Station is one station's queue (empty before its first arrival).
func (s *Service) Station(ctx context.Context, stationID string) (stationqueue.Snapshot, error) {
	st, ok := s.l.Station(stationID)
	if !ok {
		return stationqueue.Snapshot{}, ErrStationNotFound
	}
	q, err := s.d.Stations.Get(ctx, stationID)
	if errors.Is(err, ports.ErrNotFound) {
		return stationqueue.New(stationID, st.BufferLimit).Snapshot(), nil
	}
	if err != nil {
		return stationqueue.Snapshot{}, err
	}
	q.SetBufferLimit(st.BufferLimit)
	snap := q.Snapshot()
	snap.Waiting = q.Waiting()
	return snap, nil
}

// Subsystems lists every capacity snapshot by kind and id.
func (s *Service) Subsystems(ctx context.Context) ([]capacity.Snapshot, error) {
	return s.d.Capacities.List(ctx)
}

// EquipmentUnits lists the last known state of an AS/RS's units.
func (s *Service) EquipmentUnits(ctx context.Context, asrsID string) ([]facts.EquipmentState, error) {
	return s.d.Units.List(ctx, asrsID)
}
