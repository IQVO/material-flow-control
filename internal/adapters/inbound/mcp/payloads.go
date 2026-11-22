package mcp

import (
	"time"

	"github.com/IQVO/material-flow-control/internal/application/facts"
	"github.com/IQVO/material-flow-control/internal/application/usecases"
	"github.com/IQVO/material-flow-control/internal/domain/capacity"
	"github.com/IQVO/material-flow-control/internal/domain/stationqueue"
	"github.com/IQVO/material-flow-control/internal/domain/transportorder"
)

type legPayload struct {
	Seq          int    `json:"seq"`
	Attempt      int    `json:"attempt"`
	Reference    string `json:"reference"`
	Subsystem    string `json:"subsystem"`
	SubsystemID  string `json:"subsystem_id"`
	Kind         string `json:"kind"`
	From         string `json:"from,omitempty"`
	To           string `json:"to,omitempty"`
	Status       string `json:"status"`
	SubsystemRef string `json:"subsystem_ref,omitempty"`
	Robot        string `json:"robot,omitempty"`
}

type transportPayload struct {
	TransportRequestID string       `json:"transport_request_id"`
	TransportOrderID   string       `json:"transport_order_id,omitempty"`
	ContainerID        string       `json:"container_id"`
	Purpose            string       `json:"purpose"`
	Status             string       `json:"status"`
	Reason             string       `json:"reason,omitempty"`
	SubsystemReason    string       `json:"subsystem_reason,omitempty"`
	ContainerLocation  string       `json:"container_location,omitempty"`
	LegsTotal          int          `json:"legs_total"`
	LegsDone           int          `json:"legs_done"`
	Retries            int          `json:"retries"`
	Legs               []legPayload `json:"legs"`
	AcceptedAt         *time.Time   `json:"accepted_at,omitempty"`
	EndedAt            *time.Time   `json:"ended_at,omitempty"`
}

type positionPayload struct {
	Kind  string `json:"kind"`
	Code  string `json:"code,omitempty"`
	Robot string `json:"robot,omitempty"`
}

type trackPayload struct {
	ContainerID              string          `json:"container_id"`
	Position                 positionPayload `json:"position"`
	ActiveTransportRequestID string          `json:"active_transport_request_id,omitempty"`
	ClarificationReason      string          `json:"clarification_reason,omitempty"`
	UpdatedAt                time.Time       `json:"updated_at"`
	Moves                    int             `json:"moves"`
}

type stationPayload struct {
	StationID   string   `json:"station_id"`
	BufferLimit int      `json:"buffer_limit"`
	Occupied    int      `json:"occupied"`
	Occupants   []string `json:"occupants"`
	Waiting     []string `json:"waiting"`
}

type capacityPayload struct {
	Kind            string    `json:"subsystem_kind"`
	ID              string    `json:"subsystem_id"`
	Status          string    `json:"status"`
	UnitsTotal      int       `json:"units_total"`
	UnitsAvailable  int       `json:"units_available"`
	OpenWork        int       `json:"open_work"`
	CapacityPerHour int       `json:"capacity_per_hour"`
	QueueDepth      int       `json:"queue_depth"`
	MeasuredAt      time.Time `json:"measured_at"`
}

type subsystemsPayload struct {
	Subsystems []capacityPayload `json:"subsystems"`
}

type unitPayload struct {
	UnitID string    `json:"unit_id"`
	Aisle  int       `json:"aisle"`
	State  string    `json:"state"`
	At     time.Time `json:"changed_at"`
}

type unitsPayload struct {
	AsrsID string        `json:"asrs_id"`
	Units  []unitPayload `json:"units"`
}

func opt(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	u := t.UTC()
	return &u
}

func toTransport(v usecases.TransportView, o transportorder.Snapshot) transportPayload {
	out := transportPayload{TransportRequestID: v.RequestID, TransportOrderID: v.OrderID, ContainerID: v.ContainerID, Purpose: string(v.Purpose),
		Status: v.Status, Reason: v.Reason, SubsystemReason: o.SubsystemReason, ContainerLocation: v.ContainerLocation,
		LegsTotal: v.LegsTotal, LegsDone: v.LegsDone, Retries: o.Retries, Legs: []legPayload{}, AcceptedAt: opt(v.AcceptedAt), EndedAt: opt(v.EndedAt)}
	for _, l := range o.Legs {
		out.Legs = append(out.Legs, legPayload{Seq: l.Seq, Attempt: l.Attempt, Reference: l.Ref, Subsystem: string(l.Subsystem),
			SubsystemID: l.SubsystemID, Kind: string(l.Kind), From: l.From.Code, To: l.To.Code, Status: string(l.Status),
			SubsystemRef: l.SubsystemRef, Robot: l.Robot})
	}
	return out
}

func toTrack(v usecases.TrackView) trackPayload {
	s := v.Track
	out := trackPayload{ContainerID: s.ContainerID, Position: positionPayload{Kind: string(s.Position.Kind), Code: s.Position.Code,
		Robot: s.Position.Robot}, ActiveTransportRequestID: s.ActiveRequestID, UpdatedAt: s.UpdatedAt.UTC(), Moves: len(v.History)}
	if s.Clarification != nil {
		out.ClarificationReason = string(s.Clarification.Reason)
	}
	return out
}

func toStation(s stationqueue.Snapshot) stationPayload {
	q := stationqueue.Restore(s)
	out := stationPayload{StationID: s.StationID, BufferLimit: s.BufferLimit, Occupied: q.Occupied(), Occupants: []string{}, Waiting: []string{}}
	for _, o := range q.Occupants() {
		out.Occupants = append(out.Occupants, o.ContainerID)
	}
	for _, w := range q.Waiting() {
		out.Waiting = append(out.Waiting, w.ContainerID)
	}
	return out
}

func toSubsystems(snaps []capacity.Snapshot) subsystemsPayload {
	out := subsystemsPayload{Subsystems: []capacityPayload{}}
	for _, s := range snaps {
		out.Subsystems = append(out.Subsystems, capacityPayload{string(s.Kind), s.ID, string(s.Status), s.UnitsTotal, s.UnitsAvailable,
			s.OpenWork, s.CapacityPerHour, s.QueueDepth, s.MeasuredAt.UTC()})
	}
	return out
}

func toUnits(asrsID string, us []facts.EquipmentState) unitsPayload {
	out := unitsPayload{AsrsID: asrsID, Units: []unitPayload{}}
	for _, u := range us {
		out.Units = append(out.Units, unitPayload{UnitID: u.UnitID, Aisle: u.Aisle, State: u.State, At: u.At.UTC()})
	}
	return out
}
