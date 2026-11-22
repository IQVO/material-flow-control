package events

import (
	"fmt"
	"time"

	"github.com/IQVO/material-flow-control/internal/domain/capacity"
	"github.com/IQVO/material-flow-control/internal/domain/containertrack"
	"github.com/IQVO/material-flow-control/internal/domain/transportorder"
	"github.com/IQVO/material-flow-control/internal/domain/transportrequest"
)

// Payloads (apis/asyncapi.yaml, ADR 0003). Every *_at is material-flow-control's clock; no field names a robot,
// leg, node, shuttle, aisle, rack position, port or storage job (seam decision 3 "Rules").

type transportRejected struct {
	RequestID   string    `json:"transport_request_id"`
	ContainerID string    `json:"container_id"`
	Source      string    `json:"source"`
	Destination string    `json:"destination"`
	Purpose     string    `json:"purpose"`
	Reason      string    `json:"reason"`
	Detail      string    `json:"detail"`
	RejectedAt  time.Time `json:"rejected_at"`
}

type transportAccepted struct {
	OrderID     string    `json:"transport_order_id"`
	RequestID   string    `json:"transport_request_id"`
	ContainerID string    `json:"container_id"`
	Source      string    `json:"source"`
	Destination string    `json:"destination"`
	Purpose     string    `json:"purpose"`
	Priority    string    `json:"priority"`
	DueBy       time.Time `json:"due_by"`
	LegsPlanned int       `json:"legs_planned"`
	AcceptedAt  time.Time `json:"accepted_at"`
}

type containerArrived struct {
	OrderID       string    `json:"transport_order_id"`
	RequestID     string    `json:"transport_request_id"`
	ContainerID   string    `json:"container_id"`
	StationID     string    `json:"station_id"`
	Purpose       string    `json:"purpose"`
	Priority      string    `json:"priority"`
	DueBy         time.Time `json:"due_by"`
	QueuePosition int       `json:"queue_position"`
	ArrivedAt     time.Time `json:"arrived_at"`
}

type transportCompleted struct {
	OrderID     string    `json:"transport_order_id"`
	RequestID   string    `json:"transport_request_id"`
	ContainerID string    `json:"container_id"`
	Destination string    `json:"destination"`
	Purpose     string    `json:"purpose"`
	CompletedAt time.Time `json:"completed_at"`
}

type transportFailed struct {
	OrderID           string    `json:"transport_order_id"`
	RequestID         string    `json:"transport_request_id"`
	ContainerID       string    `json:"container_id"`
	Purpose           string    `json:"purpose"`
	Reason            string    `json:"reason"`
	SubsystemReason   string    `json:"subsystem_reason,omitempty"`
	ContainerLocation string    `json:"container_location,omitempty"`
	FailedAt          time.Time `json:"failed_at"`
}

type sentToClarification struct {
	ContainerID        string    `json:"container_id"`
	OrderID            string    `json:"transport_order_id,omitempty"`
	RequestID          string    `json:"transport_request_id,omitempty"`
	Reason             string    `json:"reason"`
	ScannedContainerID string    `json:"scanned_container_id,omitempty"`
	DetectedAt         string    `json:"detected_at_location"`
	ClarificationPoint string    `json:"clarification_point,omitempty"`
	SentAt             time.Time `json:"sent_at"`
}

type subsystemCapacityChanged struct {
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

func describe(e any) (occurrence, error) {
	switch ev := e.(type) {
	case transportrequest.TransportRejected:
		return occurrence{"transportrequest", "TransportRejected", ev.RequestID, ev.At, transportRejected{ev.RequestID, ev.ContainerID,
			ev.Source, ev.Destination, string(ev.Purpose), string(ev.Reason), ev.Detail, ev.At.UTC()}}, nil
	case transportorder.TransportAccepted:
		return occurrence{"transportorder", "TransportAccepted", ev.OrderID, ev.At, transportAccepted{ev.OrderID, ev.RequestID,
			ev.ContainerID, ev.Source, ev.Destination, string(ev.Purpose), string(ev.Priority), ev.DueBy.UTC(), ev.Legs, ev.At.UTC()}}, nil
	case transportorder.ContainerArrivedAtStation:
		return occurrence{"transportorder", "ContainerArrivedAtStation", ev.OrderID, ev.At, containerArrived{ev.OrderID, ev.RequestID,
			ev.ContainerID, ev.StationID, string(ev.Purpose), string(ev.Priority), ev.DueBy.UTC(), ev.QueuePosition, ev.At.UTC()}}, nil
	case transportorder.TransportCompleted:
		return occurrence{"transportorder", "TransportCompleted", ev.OrderID, ev.At, transportCompleted{ev.OrderID, ev.RequestID,
			ev.ContainerID, ev.Destination, string(ev.Purpose), ev.At.UTC()}}, nil
	case transportorder.TransportFailed:
		return occurrence{"transportorder", "TransportFailed", ev.OrderID, ev.At, transportFailed{ev.OrderID, ev.RequestID,
			ev.ContainerID, string(ev.Purpose), string(ev.Reason), ev.SubsystemReason, ev.ContainerLocation, ev.At.UTC()}}, nil
	case containertrack.ContainerSentToClarification:
		return occurrence{"containertrack", "ContainerSentToClarification", ev.ContainerID, ev.At, sentToClarification{ev.ContainerID,
			ev.OrderID, ev.RequestID, string(ev.Reason), ev.ScannedContainerID, ev.DetectedAt, ev.ClarificationPoint, ev.At.UTC()}}, nil
	case capacity.SubsystemCapacityChanged:
		s := ev.Snapshot
		return occurrence{"subsystemcapacity", "SubsystemCapacityChanged", s.Subject(), s.MeasuredAt, subsystemCapacityChanged{string(s.Kind),
			s.ID, string(s.Status), s.UnitsTotal, s.UnitsAvailable, s.OpenWork, s.CapacityPerHour, s.BufferLimit, s.Occupied,
			s.QueueDepth, s.MeasuredAt.UTC()}}, nil
	default:
		return occurrence{}, fmt.Errorf("events: no CloudEvents encoder for %T", e)
	}
}
