package transportorder

import (
	"time"

	"github.com/IQVO/material-flow-control/internal/domain/transportrequest"
)

// TransportAccepted: the request became an order (subject = order id).
type TransportAccepted struct {
	OrderID     string
	RequestID   string
	ContainerID string
	Source      string
	Destination string
	Purpose     transportrequest.Purpose
	Priority    transportrequest.Priority
	DueBy       time.Time
	Legs        int
	At          time.Time
}

// ContainerArrivedAtStation: the container is at its goods-to-person station, at QueuePosition.
type ContainerArrivedAtStation struct {
	OrderID       string
	RequestID     string
	ContainerID   string
	StationID     string
	Purpose       transportrequest.Purpose
	Priority      transportrequest.Priority
	DueBy         time.Time
	QueuePosition int
	At            time.Time
}

// TransportCompleted: the container is at the destination.
type TransportCompleted struct {
	OrderID     string
	RequestID   string
	ContainerID string
	Destination string
	Purpose     transportrequest.Purpose
	At          time.Time
}

// TransportFailed: the order ended without reaching the destination; ContainerLocation is the last confirmed
// location (empty while on a robot or unknown).
type TransportFailed struct {
	OrderID           string
	RequestID         string
	ContainerID       string
	Purpose           transportrequest.Purpose
	Reason            FailReason
	SubsystemReason   string
	ContainerLocation string
	At                time.Time
}
