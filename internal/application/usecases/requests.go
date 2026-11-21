package usecases

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/IQVO/material-flow-control/internal/application/command"
	"github.com/IQVO/material-flow-control/internal/application/ports"
	"github.com/IQVO/material-flow-control/internal/domain/capacity"
	"github.com/IQVO/material-flow-control/internal/domain/containertrack"
	"github.com/IQVO/material-flow-control/internal/domain/layout"
	"github.com/IQVO/material-flow-control/internal/domain/routing"
	"github.com/IQVO/material-flow-control/internal/domain/transportorder"
	"github.com/IQVO/material-flow-control/internal/domain/transportrequest"
)

// CancelOutcome is how a cancellation was answered.
type CancelOutcome string

const (
	CancelledNow CancelOutcome = "CANCELLED"
	CancelAsked  CancelOutcome = "CANCEL_REQUESTED"
)

// purposeAllows is decision D4.
func purposeAllows(p transportrequest.Purpose, src, dst layout.Place) bool {
	fromAsrs, toAsrs := src.Kind == layout.PlaceAsrs, dst.Kind == layout.PlaceAsrs
	toFloor := dst.Kind == layout.PlaceStation || dst.Kind == layout.PlaceHandoff
	switch p {
	case transportrequest.PurposePick:
		return dst.Kind == layout.PlaceStation
	case transportrequest.PurposePutaway, transportrequest.PurposeReturn:
		return toAsrs && !fromAsrs
	case transportrequest.PurposeReplenish:
		return fromAsrs && toFloor
	default:
		return !fromAsrs && toFloor
	}
}

func checkPurpose(req transportrequest.Request, src layout.Place, srcOK bool, dst layout.Place, dstOK bool) error {
	if !srcOK || !dstOK {
		return nil
	}
	if src.Code == dst.Code {
		return fmt.Errorf("%w: source and destination are the same place", ErrInvalidRequest)
	}
	if !purposeAllows(req.Purpose, src, dst) {
		return fmt.Errorf("%w: purpose %s does not move a container from %s to %s", ErrInvalidRequest, req.Purpose, src.Kind, dst.Kind)
	}
	return nil
}

// RequestTransport accepts or rejects a transport request at once, from local state only (decision D5); a retry
// of a recorded transport_request_id returns its original outcome (decision D7).
func (s *Service) RequestTransport(ctx context.Context, req transportrequest.Request) (TransportView, error) {
	req = req.Normalized()
	if err := req.Validate(); err != nil {
		return TransportView{}, fmt.Errorf("%w: %w", ErrInvalidRequest, err)
	}
	src, srcOK := s.l.Resolve(req.Source)
	dst, dstOK := s.l.Resolve(req.Destination)
	if err := checkPurpose(req, src, srcOK, dst, dstOK); err != nil {
		return TransportView{}, err
	}
	var view TransportView
	var rejected *RejectedError
	err := s.write(ctx, func(ctx context.Context) error {
		rec, err := s.d.Requests.Get(ctx, req.ID)
		if err == nil {
			if !rec.SameRequest(req) {
				return ErrRequestConflict
			}
			view, rejected, err = s.replay(ctx, rec)
			return err
		}
		if !errors.Is(err, ports.ErrNotFound) {
			return err
		}
		f := s.newFlow(ctx)
		plan, reason, detail, err := f.decide(req, src, srcOK, dst, dstOK)
		if err != nil {
			return err
		}
		if reason != "" {
			view, rejected, err = s.reject(ctx, req, reason, detail, f.now)
			return err
		}
		view, err = f.accept(req, src, dst, plan)
		return err
	})
	if err != nil {
		return TransportView{}, err
	}
	if rejected != nil {
		return view, rejected
	}
	return view, nil
}

func (f *flow) decide(req transportrequest.Request, src layout.Place, srcOK bool, dst layout.Place, dstOK bool) ([]routing.LegPlan, transportrequest.Reason, string, error) {
	if !srcOK || !dstOK {
		return nil, transportrequest.ReasonDestinationUnreachable, "the source or the destination is not a place of this automation", nil
	}
	plan, err := routing.Plan(f.s.l, src, dst)
	if err != nil {
		return nil, transportrequest.ReasonDestinationUnreachable, err.Error(), nil
	}
	t, err := f.track(req.ContainerID)
	if err != nil {
		return nil, "", "", err
	}
	if err := t.Dispatchable(); err != nil {
		return nil, transportrequest.ReasonContainerBusy, err.Error(), nil
	}
	if !knownAtSource(t, src) {
		return nil, transportrequest.ReasonUnknownContainer, "the container is not known at the source", nil
	}
	for _, leg := range plan {
		kind := capacity.KindAmrFleet
		if leg.Subsystem == routing.SubsystemAsrs {
			kind = capacity.KindAsrs
		}
		snap, found, err := f.s.d.Capacities.Get(f.ctx, kind, leg.SubsystemID)
		if err != nil {
			return nil, "", "", err
		}
		if found && snap.Status == capacity.StatusUnavailable {
			return nil, transportrequest.ReasonSubsystemUnavailable, fmt.Sprintf("%s %s is unavailable", kind, leg.SubsystemID), nil
		}
	}
	return plan, "", "", nil
}

// knownAtSource: a container taken from the AS/RS must be tracked there; any other container must not be inside
// the automation somewhere else (a container at a station or handoff may have been carried by a person).
func knownAtSource(t *containertrack.Track, src layout.Place) bool {
	p := t.Position()
	if src.Kind == layout.PlaceAsrs {
		return p == containertrack.At(src)
	}
	return !p.Inside() || p.Code == src.Code
}

func (f *flow) accept(req transportrequest.Request, src, dst layout.Place, plan []routing.LegPlan) (TransportView, error) {
	o, err := transportorder.Accept("to-"+f.s.d.IDs.NewID(), req, src, dst, plan, f.now)
	if err != nil {
		return TransportView{}, err
	}
	t, err := f.track(req.ContainerID)
	if err != nil {
		return TransportView{}, err
	}
	if err := t.Reserve(o.ID(), req.ID, f.now); err != nil {
		return TransportView{}, err
	}
	if !t.Position().Inside() {
		t.MoveTo(containertrack.At(src), f.now)
	}
	f.addNew(o)
	if err := f.advance(o); err != nil {
		return TransportView{}, err
	}
	rec := transportrequest.Accept(req, o.ID(), f.now)
	if err := f.s.d.Requests.Insert(f.ctx, rec); err != nil {
		return TransportView{}, err
	}
	if err := f.commit(); err != nil {
		return TransportView{}, err
	}
	return viewOf(rec, o.Snapshot(), t.Position()), nil
}

func (s *Service) reject(ctx context.Context, req transportrequest.Request, reason transportrequest.Reason, detail string, at time.Time) (TransportView, *RejectedError, error) {
	rec, ev := transportrequest.Reject(req, reason, detail, at)
	if err := s.d.Requests.Insert(ctx, rec); err != nil {
		return TransportView{}, nil, err
	}
	if err := s.d.Sink.Append(ctx, ev); err != nil {
		return TransportView{}, nil, err
	}
	return rejectedView(rec), &RejectedError{RequestID: req.ID, Reason: reason, Detail: detail}, nil
}

func (s *Service) replay(ctx context.Context, rec transportrequest.Record) (TransportView, *RejectedError, error) {
	if rec.Outcome == transportrequest.OutcomeRejected {
		return rejectedView(rec), &RejectedError{RequestID: rec.Request.ID, Reason: rec.Reason, Detail: rec.Detail}, nil
	}
	v, err := s.transportView(ctx, rec)
	return v, nil, err
}

// CancelTransport cancels a transport (decision D15).
func (s *Service) CancelTransport(ctx context.Context, requestID string) (TransportView, CancelOutcome, error) {
	var view TransportView
	var outcome CancelOutcome
	err := s.write(ctx, func(ctx context.Context) error {
		rec, err := s.d.Requests.Get(ctx, requestID)
		if errors.Is(err, ports.ErrNotFound) {
			return ErrRequestNotFound
		}
		if err != nil {
			return err
		}
		if rec.Outcome == transportrequest.OutcomeRejected {
			return fmt.Errorf("%w: the request was rejected", ErrTransportEnded)
		}
		f := s.newFlow(ctx)
		o, err := f.orderByID(rec.OrderID)
		if err != nil {
			return err
		}
		decision, leg, err := o.RequestCancel(f.now)
		switch {
		case errors.Is(err, transportorder.ErrEnded):
			return ErrTransportEnded
		case errors.Is(err, transportorder.ErrNotCancellable):
			return ErrNotCancellable
		case err != nil:
			return err
		}
		outcome = CancelAsked
		switch decision {
		case transportorder.CancelNow:
			if err := f.fail(o, transportorder.FailCancelled, ""); err != nil {
				return err
			}
			outcome = CancelledNow
		case transportorder.CancelAsk:
			f.enqueue(o.ID(), leg.Ref, command.CancelLeg)
		}
		if err := f.commit(); err != nil {
			return err
		}
		t, err := f.track(o.ContainerID())
		if err != nil {
			return err
		}
		view = viewOf(rec, o.Snapshot(), t.Position())
		return nil
	})
	return view, outcome, err
}

// ResolveClarification lifts a container's clarification hold; location, when given, is where it now is.
func (s *Service) ResolveClarification(ctx context.Context, containerID, location string) (TrackView, error) {
	var view TrackView
	err := s.write(ctx, func(ctx context.Context) error {
		t, err := s.d.Tracks.Get(ctx, containerID)
		if errors.Is(err, ports.ErrNotFound) {
			return ErrTrackNotFound
		}
		if err != nil {
			return err
		}
		var at *containertrack.Position
		if location != "" {
			p, ok := s.l.Resolve(location)
			if !ok {
				return ErrUnknownLocation
			}
			pos := containertrack.At(p)
			at = &pos
		}
		if err := t.Resolve(at, s.now()); errors.Is(err, containertrack.ErrNotInClarification) {
			return ErrNotInClarification
		}
		if err := s.d.Tracks.Save(ctx, t); err != nil {
			return err
		}
		view, err = s.trackView(ctx, t)
		return err
	})
	return view, err
}
