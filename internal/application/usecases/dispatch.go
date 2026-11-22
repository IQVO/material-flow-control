package usecases

import (
	"context"
	"errors"

	"github.com/IQVO/material-flow-control/internal/application/command"
	"github.com/IQVO/material-flow-control/internal/application/ports"
	"github.com/IQVO/material-flow-control/internal/application/subsystem"
	"github.com/IQVO/material-flow-control/internal/domain/layout"
	"github.com/IQVO/material-flow-control/internal/domain/routing"
	"github.com/IQVO/material-flow-control/internal/domain/transportorder"
)

const unreachableReason = "subsystem-unreachable"

// callResult is one subsystem call's answer, made outside any unit of work.
type callResult struct {
	accepted subsystem.Accepted
	resolved layout.Place
	err      error
	skip     bool
	wait     bool
}

// DispatchDue executes the due subsystem commands in Seq order (ADR 0002 decision 4).
func (s *Service) DispatchDue(ctx context.Context) (int, error) {
	if s.d.Asrs == nil || s.d.Amr == nil {
		return 0, errors.New("usecases: the dispatcher needs both subsystem clients")
	}
	cmds, err := s.d.Commands.Due(ctx, s.now(), s.cfg.DispatchBatch)
	if err != nil {
		return 0, err
	}
	for i, c := range cmds {
		if err := s.dispatch(ctx, c); err != nil {
			return i, err
		}
	}
	return len(cmds), nil
}

func (s *Service) dispatch(ctx context.Context, c command.Command) error {
	o, err := s.d.Orders.Get(ctx, c.OrderID)
	if errors.Is(err, ports.ErrNotFound) {
		return s.write(ctx, func(ctx context.Context) error { return s.d.Commands.Save(ctx, c.Failed(s.now(), "order not found")) })
	}
	if err != nil {
		return err
	}
	leg, ok := o.LegByRef(c.LegRef)
	if !ok {
		return s.write(ctx, func(ctx context.Context) error { return s.d.Commands.Save(ctx, c.Failed(s.now(), "leg not found")) })
	}
	res := s.call(ctx, c, o, leg)
	return s.write(ctx, func(ctx context.Context) error { return s.apply(ctx, c, res) })
}

func (s *Service) call(ctx context.Context, c command.Command, o *transportorder.Order, leg transportorder.Leg) callResult {
	switch c.Kind {
	case command.RequestLeg:
		if o.Ended() || leg.Status != transportorder.LegPending {
			return callResult{skip: true}
		}
		if leg.Subsystem == routing.SubsystemAsrs {
			req := subsystem.StorageJobRequest{Kind: string(leg.Kind), ContainerID: o.ContainerID(), Reference: leg.Ref}
			if leg.Kind == routing.KindStore {
				req.PortID = leg.From.PortID
			}
			acc, err := s.d.Asrs.CreateStorageJob(ctx, c.Key, leg.SubsystemID, req)
			return callResult{accepted: acc, err: err}
		}
		return s.callMove(ctx, c, o, leg)
	case command.CancelLeg:
		if leg.SubsystemRef == "" {
			return callResult{wait: leg.Status == transportorder.LegPending, skip: leg.Status != transportorder.LegPending}
		}
		if leg.Subsystem == routing.SubsystemAsrs {
			acc, err := s.d.Asrs.CancelStorageJob(ctx, c.Key, leg.SubsystemID, leg.SubsystemRef)
			return callResult{accepted: acc, err: err}
		}
		acc, err := s.d.Amr.CancelLeg(ctx, c.Key, leg.SubsystemID, leg.SubsystemRef)
		return callResult{accepted: acc, err: err}
	default:
		return callResult{err: s.d.Asrs.ConfirmPickup(ctx, c.Key, leg.From.AsrsID, leg.From.PortID, o.ContainerID())}
	}
}

func (s *Service) callMove(ctx context.Context, c command.Command, o *transportorder.Order, leg transportorder.Leg) callResult {
	to := leg.To
	if to.IsZero() {
		to = s.inputPort(ctx, o.Destination().AsrsID)
	}
	acc, err := s.d.Amr.RequestLeg(ctx, c.Key, leg.SubsystemID, subsystem.LegRequest{ContainerID: o.ContainerID(),
		From: leg.From.HandoffPointID, To: to.HandoffPointID, Reference: leg.Ref})
	res := callResult{accepted: acc, err: err}
	if leg.To.IsZero() {
		res.resolved = to
	}
	return res
}

// inputPort is asrs-control's recommended input port, else the layout's first one (decision D3).
func (s *Service) inputPort(ctx context.Context, asrsID string) layout.Place {
	if id, err := s.d.Asrs.RecommendedInputPort(ctx, asrsID); err == nil && id != "" {
		if p, ok := s.l.Port(asrsID, id); ok && p.PortKind == layout.PortInput {
			return p
		}
	}
	return s.l.InputPorts(asrsID)[0]
}

// apply records a call's answer. A resolved input port is kept even when the call failed, so a retry under the
// same Idempotency-Key sends the same body.
func (s *Service) apply(ctx context.Context, c command.Command, res callResult) error {
	f := s.newFlow(ctx)
	o, err := f.orderByID(c.OrderID)
	if err != nil {
		return err
	}
	if !res.resolved.IsZero() {
		_ = o.ResolveTo(c.LegRef, res.resolved)
	}
	next, err := s.outcome(f, c, o, res)
	if err != nil {
		return err
	}
	if err := s.d.Commands.Save(ctx, next); err != nil {
		return err
	}
	return f.commit()
}

func (s *Service) outcome(f *flow, c command.Command, o *transportorder.Order, res callResult) (command.Command, error) {
	switch {
	case res.skip:
		return c.Done(f.now), nil
	case res.wait:
		return s.retryOrGiveUp(f, c, o, unreachableReason, "the subsystem id is not known yet")
	case res.err == nil:
		if c.Kind == command.RequestLeg {
			_ = o.Requested(c.LegRef, res.accepted.ID, f.now)
			cancelOrphan(f, c, o)
		}
		return c.Done(f.now), nil
	}
	var refusal *subsystem.Refusal
	if errors.As(res.err, &refusal) {
		if !refusal.Retryable || (c.Kind == command.CancelLeg && subsystem.NotCancellable(refusal.Code)) {
			return s.refused(f, c, o, refusal)
		}
		return s.retryOrGiveUp(f, c, o, refusal.Code, refusal.Error())
	}
	return s.retryOrGiveUp(f, c, o, unreachableReason, res.err.Error())
}

// cancelOrphan stops a leg or storage job the subsystem accepted after its order ended meanwhile (the call runs
// outside the unit of work): Requested kept the subsystem's id on the CANCELLED leg, so a best-effort CANCEL_LEG
// (key cancel-<ref>) reaches it; otherwise the subsystem would run an orphan nobody follows (decision D12).
// carry (Task 11 review)
func cancelOrphan(f *flow, c command.Command, o *transportorder.Order) {
	if leg, ok := o.LegByRef(c.LegRef); ok && o.Ended() && leg.Status == transportorder.LegCancelled && leg.SubsystemRef != "" {
		f.enqueue(o.ID(), c.LegRef, command.CancelLeg)
	}
}

func (s *Service) refused(f *flow, c command.Command, o *transportorder.Order, r *subsystem.Refusal) (command.Command, error) {
	switch c.Kind {
	case command.RequestLeg:
		s.d.Log.ErrorContext(f.ctx, "a subsystem refused a leg for good: the layout and the subsystem disagree",
			"order", o.ID(), "leg", c.LegRef, "code", r.Code, "status", r.Status)
		if err := f.fail(o, transportorder.FailSubsystemFault, r.Code); err != nil {
			return c, err
		}
	case command.CancelLeg:
		if !o.Ended() {
			o.CancelRefused(f.now)
		}
	default:
		s.d.Log.ErrorContext(f.ctx, "asrs-control refused a pickup confirmation", "order", o.ID(), "leg", c.LegRef, "code", r.Code)
	}
	return c.Failed(f.now, r.Error()), nil
}

func (s *Service) retryOrGiveUp(f *flow, c command.Command, o *transportorder.Order, code, errText string) (command.Command, error) {
	if !c.Expired(f.now) {
		return c.Retry(f.now, s.cfg.RetryInitial, s.cfg.RetryMax, errText), nil
	}
	switch c.Kind {
	case command.RequestLeg:
		if err := f.fail(o, transportorder.FailSubsystemFault, code); err != nil {
			return c, err
		}
	case command.CancelLeg:
		if !o.Ended() {
			o.CancelRefused(f.now)
		}
	default:
		s.d.Log.ErrorContext(f.ctx, "a pickup confirmation expired: the AS/RS port may stay blocked", "order", o.ID(), "leg", c.LegRef, "error", errText)
	}
	return c.Failed(f.now, errText), nil
}
