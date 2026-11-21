package usecases

import (
	"context"

	"github.com/IQVO/material-flow-control/internal/application/command"
	"github.com/IQVO/material-flow-control/internal/application/facts"
	"github.com/IQVO/material-flow-control/internal/domain/capacity"
	"github.com/IQVO/material-flow-control/internal/domain/containertrack"
	"github.com/IQVO/material-flow-control/internal/domain/layout"
	"github.com/IQVO/material-flow-control/internal/domain/transportorder"
)

// OnLegAssigned records the robot of a MOVE leg.
func (s *Service) OnLegAssigned(ctx context.Context, eventID, eventType string, f facts.LegAssigned) error {
	return s.consume(ctx, eventID, eventType, func(ctx context.Context) error {
		fl := s.newFlow(ctx)
		o, err := fl.orderByRef(f.Reference)
		if err != nil {
			return err
		}
		if o != nil {
			_ = o.Assigned(f.Reference, f.Robot, fl.now)
		}
		return fl.commit()
	})
}

// OnContainerPickedUp: the container is on a robot; a pickup from an AS/RS output port is confirmed to asrs-control
// (decision D10); a pickup from a station frees its slot (decision D11).
func (s *Service) OnContainerPickedUp(ctx context.Context, eventID, eventType string, f facts.ContainerPickedUp) error {
	return s.consume(ctx, eventID, eventType, func(ctx context.Context) error {
		fl := s.newFlow(ctx)
		t, err := fl.track(f.ContainerID)
		if err != nil {
			return err
		}
		t.MoveTo(containertrack.OnRobotOf(f.FleetID, f.Robot), fl.now)
		from, known := s.l.ByHandoffPoint(f.From)
		o, err := fl.orderByRef(f.Reference)
		if err != nil {
			return err
		}
		if o != nil {
			_ = o.PickedUp(f.Reference, f.Robot, fl.now)
			if known && from.Kind == layout.PlacePort && from.PortKind == layout.PortOutput {
				fl.enqueue(o.ID(), f.Reference, command.ConfirmPickup)
			}
		}
		if known && from.Kind == layout.PlaceStation {
			if err := leaveStation(fl, from, f.ContainerID); err != nil {
				return err
			}
		}
		return fl.commit()
	})
}

// leaveStation frees the container's station slot and admits the next waiting order (decision D11). It is the
// brief's block moved out of OnContainerPickedUp for gocognit (max 20), statements unchanged (Task 10 report D1).
func leaveStation(fl *flow, from layout.Place, containerID string) error {
	q, err := fl.queue(from.StationID)
	if err != nil {
		return err
	}
	if q.Leave(containerID) {
		if err := fl.admit(q); err != nil {
			return err
		}
	}
	return nil
}

// OnLegCompleted: the container is at the MOVE's destination; the next leg starts or the transport completes.
func (s *Service) OnLegCompleted(ctx context.Context, eventID, eventType string, f facts.LegCompleted) error {
	return s.consume(ctx, eventID, eventType, func(ctx context.Context) error {
		fl := s.newFlow(ctx)
		to, known := s.l.ByHandoffPoint(f.To)
		t, err := fl.track(f.ContainerID)
		if err != nil {
			return err
		}
		if known {
			t.MoveTo(containertrack.At(to), fl.now)
		}
		o, err := fl.orderByRef(f.Reference)
		if err != nil {
			return err
		}
		if o == nil {
			return fl.commit()
		}
		if leg, ok := o.LegByRef(f.Reference); ok && leg.To.IsZero() && known {
			_ = o.ResolveTo(f.Reference, to)
		}
		if o.Delivered(f.Reference, fl.now) != nil {
			return fl.commit()
		}
		if o.CancelRequested() {
			o.CancelRefused(fl.now)
		}
		if err := fl.advance(o); err != nil {
			return err
		}
		return fl.commit()
	})
}

func retryableLegFailure(reason string) bool {
	switch reason {
	case "ROBOT_BLOCKED", "ROBOT_FAULT", "ROBOT_OFFLINE", "ORDER_REJECTED", "MAP_VERSION_RETIRED":
		return true
	}
	return false
}

// OnLegFailed maps a failed MOVE (decisions D12, D13, D15).
func (s *Service) OnLegFailed(ctx context.Context, eventID, eventType string, f facts.LegFailed) error {
	return s.consume(ctx, eventID, eventType, func(ctx context.Context) error {
		fl := s.newFlow(ctx)
		o, err := fl.orderByRef(f.Reference)
		if err != nil {
			return err
		}
		if o == nil {
			return fl.commit()
		}
		if o.LegFailed(f.Reference, fl.now) != nil {
			if err := endedOnRobot(fl, o, f); err != nil {
				return err
			}
			return fl.commit()
		}
		leg, _ := o.LegByRef(f.Reference)
		if err := s.legFailed(fl, o, leg, f); err != nil {
			return err
		}
		return fl.commit()
	})
}

// endedOnRobot follows a container a robot still carries after its order ended (e.g. Tick failed it TIMEOUT and
// the best-effort CANCEL_LEG answered CANCELLED with the load): the track moves onto the robot and is held
// CONTAINER_ON_FAILED_ROBOT; an already-held track keeps its first hold (ruling P4-R5). The order is unchanged.
// carry (Task 10 review): decision D16, the track follows physical facts even for an ended order.
func endedOnRobot(fl *flow, o *transportorder.Order, f facts.LegFailed) error {
	// carry (Task 11 review): the live path's predicate (legFailed); the track moves only onto a named robot.
	if !o.Ended() || (!f.ContainerOnRobot && f.Reason != "DROP_FAILED") {
		return nil
	}
	t, err := fl.track(o.ContainerID())
	if err != nil {
		return err
	}
	if f.Robot != "" {
		t.MoveTo(containertrack.OnRobotOf(f.FleetID, f.Robot), fl.now)
	}
	// carry (Task 11 review): an internal order's container is already held (P4-R5) and has no re-request.
	if o.Internal() {
		return nil
	}
	// carry (Task 11 review): read the re-request BEFORE clarify, which may reserve the track for its own
	// internal clarification order.
	reReq := t.ActiveOrderID()
	leg, _ := o.LegByRef(f.Reference)
	if err := fl.clarify(o, containertrack.Clarification{Reason: containertrack.ReasonOnFailedRobot}, leg.From, transportorder.FailSubsystemFault, f.Reason); err != nil {
		return err
	}
	return failReRequest(fl, reReq, o.ID())
}

// failReRequest ends the order that re-requested a container which is in fact on a robot and now held
// (responsibility 4: a held container is never dispatched): FAILED CLARIFICATION, best-effort CANCEL_LEG.
// carry (Task 11 review): id is the track's active order read before clarify.
func failReRequest(fl *flow, id, endedID string) error {
	if id == "" || id == endedID {
		return nil
	}
	b, err := fl.orderByID(id)
	if err != nil {
		return err
	}
	if cur, ok := b.Current(); ok && cur.SubsystemRef != "" {
		fl.enqueue(b.ID(), cur.Ref, command.CancelLeg)
	}
	return fl.fail(b, transportorder.FailClarification, string(containertrack.ReasonOnFailedRobot))
}

func (s *Service) legFailed(fl *flow, o *transportorder.Order, leg transportorder.Leg, f facts.LegFailed) error {
	switch {
	case f.Reason == "CANCELLED" && o.CancelRequested():
		// carry (plan 03 Task 9 review): a cancelled leg may end with the container on the robot; the track follows it.
		if f.ContainerOnRobot && f.Robot != "" {
			t, err := fl.track(o.ContainerID())
			if err != nil {
				return err
			}
			t.MoveTo(containertrack.OnRobotOf(f.FleetID, f.Robot), fl.now)
			// carry (Task 10 review): a leg that failed with the container on the robot holds it (decision D12).
			return fl.clarify(o, containertrack.Clarification{Reason: containertrack.ReasonOnFailedRobot}, leg.From, transportorder.FailCancelled, "")
		}
		return fl.fail(o, transportorder.FailCancelled, "")
	case f.ContainerOnRobot || f.Reason == "DROP_FAILED":
		if f.Robot != "" {
			t, err := fl.track(o.ContainerID())
			if err != nil {
				return err
			}
			t.MoveTo(containertrack.OnRobotOf(f.FleetID, f.Robot), fl.now)
		}
		return fl.clarify(o, containertrack.Clarification{Reason: containertrack.ReasonOnFailedRobot}, leg.From, transportorder.FailSubsystemFault, f.Reason)
	case f.Reason == "PICK_FAILED":
		return fl.clarify(o, containertrack.Clarification{Reason: containertrack.ReasonNotAtSource}, leg.From, transportorder.FailClarification, f.Reason)
	case retryableLegFailure(f.Reason) && o.Retries() < s.cfg.LegRetryLimit && !o.CancelRequested():
		if next, err := o.RetryLeg(leg.Ref, fl.now); err == nil {
			fl.enqueue(o.ID(), next.Ref, command.RequestLeg)
			return nil
		}
	}
	return fl.fail(o, transportorder.FailSubsystemFault, f.Reason)
}

// OnFleetCapacityChanged refreshes the AMR fleet capacity snapshot (decision D17: robots_available is the
// available count, legs_queued + legs_active the open work, robots_total 0 is accepted, changed_at is never copied).
func (s *Service) OnFleetCapacityChanged(ctx context.Context, eventID, eventType string, f facts.FleetCapacity) error {
	return s.consume(ctx, eventID, eventType, func(ctx context.Context) error {
		fleet := s.l.Fleet()
		if f.FleetID != fleet.FleetID {
			s.skip(ctx, eventType, "unknown fleet", f.FleetID)
			return nil
		}
		fl := s.newFlow(ctx)
		fl.publishCapacity(capacity.Fleet(f.FleetID, f.RobotsTotal, f.RobotsOnline, f.RobotsAvailable, f.LegsQueued, f.LegsActive, fleet.LegsPerRobotHour, fl.now))
		return fl.commit()
	})
}
