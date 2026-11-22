package usecases

import (
	"context"
	"errors"
	"sort"
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
	o, err := s.order(ctx, rec)
	if err != nil {
		return TransportView{}, err
	}
	pos, err := s.position(ctx, rec.Request.ContainerID)
	if err != nil {
		return TransportView{}, err
	}
	return viewOf(rec, o.Snapshot(), pos), nil
}

// order reads the order of an ACCEPTED record. ruling P4-R22: the record outlives its order (the retention purges
// ended orders, ADR 0008), so a missing order is the expired answer, never a 500. A purge committing between the
// record read and this one (no shared snapshot outside a unit of work) answers the same.
func (s *Service) order(ctx context.Context, rec transportrequest.Record) (*transportorder.Order, error) {
	o, err := s.d.Orders.Get(ctx, rec.OrderID)
	if errors.Is(err, ports.ErrNotFound) {
		return nil, ErrTransportExpired
	}
	return o, err
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
	o, err := s.order(ctx, rec)
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

// Subsystems lists every capacity snapshot by kind and id. A station is persisted only when a flow
// first touches its queue (commit's saveAggregates), but the read contract is "one per layout entry"
// (openapi listSubsystems): a station nobody delivered to yet is derived from the layout, empty and
// AVAILABLE — the same derivation Station() makes for an untouched queue. Deploy verification
// 2026-11-22: without this, STATION/ST1 was absent from GET /subsystems on a fresh floor.
func (s *Service) Subsystems(ctx context.Context) ([]capacity.Snapshot, error) {
	list, err := s.d.Capacities.List(ctx)
	if err != nil {
		return nil, err
	}
	persisted := make(map[string]bool, len(list))
	for _, snap := range list {
		if snap.Kind == capacity.KindStation {
			persisted[snap.ID] = true
		}
	}
	for _, st := range s.l.Stations() {
		if !persisted[st.StationID] {
			list = append(list, capacity.Station(st.StationID, st.BufferLimit, 0, 0, s.now()))
		}
	}
	sort.Slice(list, func(i, j int) bool {
		return string(list[i].Kind)+"/"+list[i].ID < string(list[j].Kind)+"/"+list[j].ID
	})
	return list, nil
}

// EquipmentUnits lists the last known state of an AS/RS's units. carry (Task 20 review): an AS/RS the layout does
// not name is ErrAsrsNotFound, so an empty list always means "no unit reported yet", never "no such AS/RS".
func (s *Service) EquipmentUnits(ctx context.Context, asrsID string) ([]facts.EquipmentState, error) {
	if _, ok := s.l.Asrs(asrsID); !ok {
		return nil, ErrAsrsNotFound
	}
	return s.d.Units.List(ctx, asrsID)
}
