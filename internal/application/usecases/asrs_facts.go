package usecases

import (
	"context"
	"errors"

	"github.com/IQVO/material-flow-control/internal/application/facts"
	"github.com/IQVO/material-flow-control/internal/domain/capacity"
	"github.com/IQVO/material-flow-control/internal/domain/containertrack"
	"github.com/IQVO/material-flow-control/internal/domain/layout"
	"github.com/IQVO/material-flow-control/internal/domain/routing"
	"github.com/IQVO/material-flow-control/internal/domain/transportorder"
)

func (s *Service) skip(ctx context.Context, eventType, why, id string) {
	s.d.Log.DebugContext(ctx, "fact skipped", "type", eventType, "reason", why, "id", id)
}

// OnContainerStored: the container is in the AS/RS (the registry learns it, decision D6); a STORE leg of ours ends.
func (s *Service) OnContainerStored(ctx context.Context, eventID, eventType string, f facts.ContainerStored) error {
	return s.consume(ctx, eventID, eventType, func(ctx context.Context) error {
		asrs, ok := s.l.AsrsPlace(f.AsrsID)
		if !ok {
			s.skip(ctx, eventType, "unknown AS/RS", f.AsrsID)
			return nil
		}
		fl := s.newFlow(ctx)
		t, err := fl.track(f.ContainerID)
		if err != nil {
			return err
		}
		t.MoveTo(containertrack.At(asrs), fl.now)
		o, err := fl.orderByRef(f.Reference)
		if err != nil {
			return err
		}
		if o != nil && o.Stored(f.Reference, fl.now) == nil {
			if err := fl.advance(o); err != nil {
				return err
			}
		}
		return fl.commit()
	})
}

// OnContainerPresented: a RETRIEVE leg ends at its output port; a READ starts the next leg, a NO_READ or a
// MISMATCH sends the container to clarification (decision D12).
func (s *Service) OnContainerPresented(ctx context.Context, eventID, eventType string, f facts.ContainerPresented) error {
	return s.consume(ctx, eventID, eventType, func(ctx context.Context) error {
		port, ok := s.l.Port(f.AsrsID, f.PortID)
		if !ok {
			s.skip(ctx, eventType, "unknown port", f.AsrsID+"/"+f.PortID)
			return nil
		}
		fl := s.newFlow(ctx)
		t, err := fl.track(f.ContainerID)
		if err != nil {
			return err
		}
		o, err := fl.orderByRef(f.Reference)
		if err != nil {
			return err
		}
		// carry (Task 10 review): a presentation re-emitted under a new event id after its RETRIEVE ended is stale:
		// the container has moved on (a robot may hold it), so the track keeps its position.
		if stalePresentation(o, f.Reference) {
			s.skip(ctx, eventType, "stale presentation of a finished retrieve", f.Reference)
			return fl.commit()
		}
		t.MoveTo(containertrack.At(port), fl.now)
		if o == nil || o.Presented(f.Reference, port, fl.now) != nil {
			return fl.commit()
		}
		if err := presentedScan(fl, o, f, port); err != nil {
			return err
		}
		return fl.commit()
	})
}

// presentedScan acts on the scan of a presentation that ended its RETRIEVE: READ advances the order (a pending
// cancellation is refused: the job finished), NO_READ and MISMATCH send the container to clarification. It is the
// brief's block moved out of OnContainerPresented for gocognit (max 20), statements unchanged.
func presentedScan(fl *flow, o *transportorder.Order, f facts.ContainerPresented, port layout.Place) error {
	if f.ScanResult == "READ" {
		if o.CancelRequested() {
			o.CancelRefused(fl.now)
		}
		return fl.advance(o)
	}
	reason := containertrack.ReasonNoRead
	if f.ScanResult == "MISMATCH" {
		reason = containertrack.ReasonMismatch
	}
	return fl.clarify(o, containertrack.Clarification{Reason: reason, ScannedContainerID: f.ScannedContainerID}, port,
		transportorder.FailClarification, f.ScanResult)
}

// stalePresentation reports whether ref names a RETRIEVE of o that already ended DONE (its presentation was applied).
func stalePresentation(o *transportorder.Order, ref string) bool {
	if o == nil {
		return false
	}
	leg, ok := o.LegByRef(ref)
	return ok && leg.Kind == routing.KindRetrieve && leg.Status == transportorder.LegDone
}

// OnStorageJobFailed maps a failed store or retrieve (decisions D12, D13, D15).
func (s *Service) OnStorageJobFailed(ctx context.Context, eventID, eventType string, f facts.StorageJobFailed) error {
	return s.consume(ctx, eventID, eventType, func(ctx context.Context) error {
		fl := s.newFlow(ctx)
		o, err := fl.orderByRef(f.Reference)
		if err != nil {
			return err
		}
		if o == nil {
			return fl.commit()
		}
		// carry (Task 10 review): a StorageJobFailed ends only a RETRIEVE or a STORE.
		if err := o.LegFailedAs(f.Reference, fl.now, routing.KindRetrieve, routing.KindStore); err != nil {
			if errors.Is(err, transportorder.ErrWrongLeg) {
				s.skip(ctx, eventType, "fact does not match the leg's kind", f.Reference)
			}
			return fl.commit()
		}
		leg, _ := o.LegByRef(f.Reference)
		if err := s.storageJobFailed(fl, o, leg, f.Reason); err != nil {
			return err
		}
		return fl.commit()
	})
}

func (s *Service) storageJobFailed(fl *flow, o *transportorder.Order, leg transportorder.Leg, reason string) error {
	switch reason {
	case "CANCELLED":
		if o.CancelRequested() {
			return fl.fail(o, transportorder.FailCancelled, "")
		}
		return fl.fail(o, transportorder.FailSubsystemFault, reason)
	case "CONTAINER_NOT_AT_PORT":
		return fl.clarify(o, containertrack.Clarification{Reason: containertrack.ReasonNotAtPort}, leg.From, transportorder.FailClarification, reason)
	case "STORAGE_MAP_MISMATCH":
		return fl.clarify(o, containertrack.Clarification{Reason: containertrack.ReasonStorageMapMismatch}, leg.From, transportorder.FailClarification, reason)
	case "EQUIPMENT_FAULT", "EQUIPMENT_UNREACHABLE", "PORT_OCCUPIED":
		if leg.Kind == routing.KindStore && o.Replans() < 1 {
			if plan, err := routing.StoreViaOtherInput(s.l, leg.From, leg.To); err == nil && o.Replan(leg.Ref, plan, fl.now) == nil {
				return fl.advance(o)
			}
		}
	}
	return fl.fail(o, transportorder.FailSubsystemFault, reason)
}

// OnEquipmentStateChanged keeps the unit's state for the read views; capacity comes from AsrsCapacityChanged.
func (s *Service) OnEquipmentStateChanged(ctx context.Context, eventID, eventType string, f facts.EquipmentState) error {
	return s.consume(ctx, eventID, eventType, func(ctx context.Context) error {
		f.At = s.now()
		return s.d.Units.Save(ctx, f)
	})
}

// OnAsrsCapacityChanged refreshes the AS/RS capacity snapshot (decision D17: units_available is the available
// count, open_jobs the open work, measured_at is never copied: the snapshot carries this context's clock).
func (s *Service) OnAsrsCapacityChanged(ctx context.Context, eventID, eventType string, f facts.AsrsCapacity) error {
	return s.consume(ctx, eventID, eventType, func(ctx context.Context) error {
		a, ok := s.l.Asrs(f.AsrsID)
		if !ok {
			s.skip(ctx, eventType, "unknown AS/RS", f.AsrsID)
			return nil
		}
		fl := s.newFlow(ctx)
		fl.publishCapacity(capacity.Asrs(f.AsrsID, f.UnitsTotal, f.UnitsAvailable, f.OpenJobs, a.RetrievalsPerUnitHour, fl.now))
		return fl.commit()
	})
}
