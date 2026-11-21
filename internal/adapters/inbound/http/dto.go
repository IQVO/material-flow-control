package http

import (
	"time"

	"github.com/IQVO/material-flow-control/internal/application/usecases"
	"github.com/IQVO/material-flow-control/internal/domain/capacity"
	"github.com/IQVO/material-flow-control/internal/domain/containertrack"
	"github.com/IQVO/material-flow-control/internal/domain/stationqueue"
	"github.com/IQVO/material-flow-control/internal/domain/transportrequest"
)

type transportRequestBody struct {
	TransportRequestID string `json:"transport_request_id"`
	ContainerID        string `json:"container_id"`
	Source             string `json:"source"`
	Destination        string `json:"destination"`
	Purpose            string `json:"purpose"`
	Priority           string `json:"priority"`
	DueBy              string `json:"due_by"`
}

func (b transportRequestBody) request() (transportrequest.Request, error) {
	due, err := time.Parse(time.RFC3339, b.DueBy)
	if err != nil {
		return transportrequest.Request{}, errInvalidParameter
	}
	return transportrequest.Request{ID: b.TransportRequestID, ContainerID: b.ContainerID, Source: b.Source, Destination: b.Destination,
		Purpose: transportrequest.Purpose(b.Purpose), Priority: transportrequest.Priority(b.Priority), DueBy: due.UTC()}, nil
}

type resolutionBody struct {
	Location string `json:"location"`
}

type transportOrderDTO struct {
	TransportRequestID string     `json:"transport_request_id"`
	TransportOrderID   string     `json:"transport_order_id,omitempty"`
	ContainerID        string     `json:"container_id"`
	Source             string     `json:"source"`
	Destination        string     `json:"destination"`
	Purpose            string     `json:"purpose"`
	Priority           string     `json:"priority"`
	DueBy              time.Time  `json:"due_by"`
	Status             string     `json:"status"`
	Reason             string     `json:"reason,omitempty"`
	ContainerLocation  string     `json:"container_location,omitempty"`
	LegsTotal          int        `json:"legs_total"`
	LegsDone           int        `json:"legs_done"`
	AcceptedAt         *time.Time `json:"accepted_at,omitempty"`
	RejectedAt         *time.Time `json:"rejected_at,omitempty"`
	EndedAt            *time.Time `json:"ended_at,omitempty"`
}

func optTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	u := t.UTC()
	return &u
}

func toTransport(v usecases.TransportView) transportOrderDTO {
	return transportOrderDTO{TransportRequestID: v.RequestID, TransportOrderID: v.OrderID, ContainerID: v.ContainerID, Source: v.Source,
		Destination: v.Destination, Purpose: string(v.Purpose), Priority: string(v.Priority), DueBy: v.DueBy.UTC(), Status: v.Status,
		Reason: v.Reason, ContainerLocation: v.ContainerLocation, LegsTotal: v.LegsTotal, LegsDone: v.LegsDone,
		AcceptedAt: optTime(v.AcceptedAt), RejectedAt: optTime(v.RejectedAt), EndedAt: optTime(v.EndedAt)}
}

// positionDTO never carries the robot key (no equipment vocabulary to the WES; MCP and A2A show it).
type positionDTO struct {
	Kind string `json:"kind"`
	Code string `json:"code,omitempty"`
}

type clarificationDTO struct {
	Reason             string    `json:"reason"`
	DetectedAtLocation string    `json:"detected_at_location"`
	ScannedContainerID string    `json:"scanned_container_id,omitempty"`
	Since              time.Time `json:"since"`
}

type moveDTO struct {
	From               positionDTO `json:"from"`
	To                 positionDTO `json:"to"`
	TransportRequestID string      `json:"transport_request_id,omitempty"`
	At                 time.Time   `json:"at"`
}

type containerTrackDTO struct {
	ContainerID              string            `json:"container_id"`
	Position                 positionDTO       `json:"position"`
	ActiveTransportRequestID string            `json:"active_transport_request_id,omitempty"`
	Clarification            *clarificationDTO `json:"clarification,omitempty"`
	UpdatedAt                time.Time         `json:"updated_at"`
	History                  []moveDTO         `json:"history"`
}

func toPosition(p containertrack.Position) positionDTO {
	return positionDTO{Kind: string(p.Kind), Code: p.Code}
}

func toTrack(v usecases.TrackView) containerTrackDTO {
	s := v.Track
	out := containerTrackDTO{ContainerID: s.ContainerID, Position: toPosition(s.Position), ActiveTransportRequestID: s.ActiveRequestID,
		UpdatedAt: s.UpdatedAt.UTC(), History: []moveDTO{}}
	if c := s.Clarification; c != nil {
		out.Clarification = &clarificationDTO{Reason: string(c.Reason), DetectedAtLocation: c.DetectedAt,
			ScannedContainerID: c.ScannedContainerID, Since: c.Since.UTC()}
	}
	for _, m := range v.History {
		out.History = append(out.History, moveDTO{From: toPosition(m.From), To: toPosition(m.To), TransportRequestID: m.RequestID, At: m.At.UTC()})
	}
	return out
}

type occupantDTO struct {
	ContainerID        string    `json:"container_id"`
	TransportRequestID string    `json:"transport_request_id"`
	Position           int       `json:"position"`
	AdmittedAt         time.Time `json:"admitted_at"`
}

type waitingDTO struct {
	ContainerID        string    `json:"container_id"`
	TransportRequestID string    `json:"transport_request_id"`
	Priority           string    `json:"priority"`
	DueBy              time.Time `json:"due_by"`
	ReadyAt            time.Time `json:"ready_at"`
}

type stationQueueDTO struct {
	StationID   string        `json:"station_id"`
	BufferLimit int           `json:"buffer_limit"`
	Occupied    int           `json:"occupied"`
	Occupants   []occupantDTO `json:"occupants"`
	Waiting     []waitingDTO  `json:"waiting"`
}

func toStation(s stationqueue.Snapshot) stationQueueDTO {
	q := stationqueue.Restore(s)
	out := stationQueueDTO{StationID: s.StationID, BufferLimit: s.BufferLimit, Occupied: q.Occupied(), Occupants: []occupantDTO{}, Waiting: []waitingDTO{}}
	for _, o := range q.Occupants() {
		out.Occupants = append(out.Occupants, occupantDTO{ContainerID: o.ContainerID, TransportRequestID: o.RequestID,
			Position: q.Position(o.ContainerID), AdmittedAt: o.AdmittedAt.UTC()})
	}
	for _, w := range q.Waiting() {
		out.Waiting = append(out.Waiting, waitingDTO{ContainerID: w.ContainerID, TransportRequestID: w.RequestID, Priority: string(w.Priority),
			DueBy: w.DueBy.UTC(), ReadyAt: w.ReadyAt.UTC()})
	}
	return out
}

type subsystemCapacityDTO struct {
	Kind            string    `json:"subsystem_kind"`
	ID              string    `json:"subsystem_id"`
	Status          string    `json:"status"`
	UnitsTotal      int       `json:"units_total"`
	UnitsAvailable  int       `json:"units_available"`
	OpenWork        int       `json:"open_work"`
	CapacityPerHour int       `json:"capacity_per_hour"`
	BufferLimit     int       `json:"buffer_limit"`
	Occupied        int       `json:"occupied"`
	QueueDepth      int       `json:"queue_depth"`
	MeasuredAt      time.Time `json:"measured_at"`
}

type subsystemsDTO struct {
	Subsystems []subsystemCapacityDTO `json:"subsystems"`
}

func toSubsystems(snaps []capacity.Snapshot) subsystemsDTO {
	out := subsystemsDTO{Subsystems: []subsystemCapacityDTO{}}
	for _, s := range snaps {
		out.Subsystems = append(out.Subsystems, subsystemCapacityDTO{string(s.Kind), s.ID, string(s.Status), s.UnitsTotal,
			s.UnitsAvailable, s.OpenWork, s.CapacityPerHour, s.BufferLimit, s.Occupied, s.QueueDepth, s.MeasuredAt.UTC()})
	}
	return out
}
