// Package capacity is the subsystem capacity view (decision D17, ADR 0006): one snapshot per subsystem and
// station, aggregated from asrs-control's AsrsCapacityChanged, amr-fleet-control's FleetCapacityChanged and the
// station queues, published as SubsystemCapacityChanged on every change of a published field.
package capacity

import "time"

// Kind is what a snapshot measures.
type Kind string

const (
	KindAmrFleet Kind = "AMR_FLEET"
	KindAsrs     Kind = "ASRS"
	KindStation  Kind = "STATION"
)

// Status is the release-relevant summary.
type Status string

const (
	StatusAvailable   Status = "AVAILABLE"
	StatusDegraded    Status = "DEGRADED"
	StatusUnavailable Status = "UNAVAILABLE"
)

// Snapshot is one subsystem's or station's capacity at MeasuredAt.
type Snapshot struct {
	Kind            Kind
	ID              string
	Status          Status
	UnitsTotal      int
	UnitsAvailable  int
	OpenWork        int
	CapacityPerHour int
	BufferLimit     int
	Occupied        int
	QueueDepth      int
	MeasuredAt      time.Time
}

// Subject is the CloudEvents subject `<kind>/<id>`.
func (s Snapshot) Subject() string { return string(s.Kind) + "/" + s.ID }

// SameFacts compares every published field but the measurement time.
func (s Snapshot) SameFacts(o Snapshot) bool {
	s.MeasuredAt, o.MeasuredAt = time.Time{}, time.Time{}
	return s == o
}

func nonNegative(v int) int { return max(v, 0) }

func status(available, total int, unavailable bool) Status {
	if unavailable {
		return StatusUnavailable
	}
	if available < total {
		return StatusDegraded
	}
	return StatusAvailable
}

// Asrs is an AS/RS snapshot from asrs-control's AsrsCapacityChanged (D17 mapping): unitsAvailable is
// asrs-control's units_available (not HELD: an aisle unit queues jobs, so a working unit is dispatchable),
// openJobs its queued + executing jobs. Unavailable when no unit is available; a units_total of 0 is accepted.
func Asrs(asrsID string, unitsTotal, unitsAvailable, openJobs, perUnitHour int, at time.Time) Snapshot {
	avail := nonNegative(unitsAvailable)
	return Snapshot{Kind: KindAsrs, ID: asrsID, Status: status(avail, unitsTotal, avail == 0), UnitsTotal: unitsTotal,
		UnitsAvailable: avail, OpenWork: nonNegative(openJobs), CapacityPerHour: avail * perUnitHour, MeasuredAt: at}
}

// Fleet is the AMR fleet snapshot from amr-fleet-control's FleetCapacityChanged (D17 mapping): available is
// robots_available (idle, assignable, at or above the charge threshold), open work legs_queued + legs_active.
// Unavailable only when no robot is online (amr-fleet-control then refuses every leg with fleet-unavailable),
// which includes a fleet of 0 robots (accepted, every count 0); degraded when fewer robots are available than exist.
func Fleet(fleetID string, total, online, available, queued, active, perRobotHour int, at time.Time) Snapshot {
	avail := nonNegative(available)
	return Snapshot{Kind: KindAmrFleet, ID: fleetID, Status: status(avail, total, online <= 0), UnitsTotal: total,
		UnitsAvailable: avail, OpenWork: nonNegative(queued) + nonNegative(active), CapacityPerHour: avail * perRobotHour, MeasuredAt: at}
}

// Station is a station snapshot: degraded while arrivals wait upstream for a slot.
func Station(stationID string, limit, occupied, waiting int, at time.Time) Snapshot {
	st := StatusAvailable
	if waiting > 0 {
		st = StatusDegraded
	}
	return Snapshot{Kind: KindStation, ID: stationID, Status: st, UnitsTotal: limit, UnitsAvailable: nonNegative(limit - occupied),
		OpenWork: occupied + waiting, BufferLimit: limit, Occupied: occupied, QueueDepth: waiting, MeasuredAt: at}
}

// SubsystemCapacityChanged is published for a changed snapshot (entity segment subsystemcapacity).
type SubsystemCapacityChanged struct{ Snapshot }

// Changed returns the event when next differs from the last published snapshot (or none was published).
func Changed(prev Snapshot, found bool, next Snapshot) (SubsystemCapacityChanged, bool) {
	if found && prev.SameFacts(next) {
		return SubsystemCapacityChanged{}, false
	}
	return SubsystemCapacityChanged{Snapshot: next}, true
}
