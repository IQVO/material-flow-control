// Package facts holds the subsystem facts the Kafka consumers decode (ADR 0004), as plain values. At is the
// producer's own `*_at` field, kept for logs and the read views only; every `*_at` this context publishes is its
// own clock (Global Constraints, time rule).
package facts

import "time"

// ContainerStored is asrs-control's ContainerStored.
type ContainerStored struct {
	JobID       string
	AsrsID      string
	ContainerID string
	PortID      string
	Reference   string
	At          time.Time
}

// ContainerPresented is asrs-control's ContainerPresentedAtPort.
type ContainerPresented struct {
	JobID              string
	AsrsID             string
	ContainerID        string
	PortID             string
	Reference          string
	ScanResult         string
	ScannedContainerID string
	At                 time.Time
}

// StorageJobFailed is asrs-control's StorageJobFailed.
type StorageJobFailed struct {
	JobID       string
	AsrsID      string
	Kind        string
	ContainerID string
	PortID      string
	Reference   string
	Reason      string
	PLCReason   string
	At          time.Time
}

// EquipmentState is asrs-control's EquipmentStateChanged, kept per unit for the read views.
type EquipmentState struct {
	AsrsID        string
	UnitID        string
	Aisle         int
	State         string
	PreviousState string
	At            time.Time
}

// AsrsCapacity is asrs-control's AsrsCapacityChanged.
type AsrsCapacity struct {
	AsrsID         string
	UnitsTotal     int
	UnitsAvailable int
	PositionsTotal int
	PositionsFree  int
	OpenJobs       int
	At             time.Time
}

// LegAssigned is amr-fleet-control's LegAssigned; Robot is `<manufacturer>/<serial_number>`.
type LegAssigned struct {
	LegID       string
	FleetID     string
	ContainerID string
	From        string
	To          string
	Reference   string
	Robot       string
	At          time.Time
}

// ContainerPickedUp is amr-fleet-control's ContainerPickedUp.
type ContainerPickedUp struct {
	LegID       string
	FleetID     string
	ContainerID string
	From        string
	Reference   string
	Robot       string
	At          time.Time
}

// LegCompleted is amr-fleet-control's LegCompleted.
type LegCompleted struct {
	LegID       string
	FleetID     string
	ContainerID string
	To          string
	Reference   string
	Robot       string
	At          time.Time
}

// LegFailed is amr-fleet-control's LegFailed.
type LegFailed struct {
	LegID            string
	FleetID          string
	ContainerID      string
	From             string
	To               string
	Reference        string
	Robot            string
	Reason           string
	RobotError       string
	ContainerOnRobot bool
	At               time.Time
}

// FleetCapacity is amr-fleet-control's FleetCapacityChanged.
type FleetCapacity struct {
	FleetID         string
	RobotsTotal     int
	RobotsOnline    int
	RobotsAvailable int
	RobotsCharging  int
	RobotsHeld      int
	LegsQueued      int
	LegsActive      int
	At              time.Time
}
