package kafka

import (
	"context"
	"errors"
	"time"

	"github.com/IQVO/material-flow-control/internal/application/facts"
)

// The asrs-control payloads (apis/asyncapi.yaml at 007bf7b). plc_timestamp is ignored (tolerant reader).

type containerStoredData struct {
	StorageJobID string    `json:"storage_job_id"`
	AsrsID       string    `json:"asrs_id"`
	ContainerID  string    `json:"container_id"`
	PortID       string    `json:"port_id"`
	Reference    string    `json:"reference"`
	StoredAt     time.Time `json:"stored_at"`
}

func (d containerStoredData) check() error {
	return required("storage_job_id", d.StorageJobID, "asrs_id", d.AsrsID, "container_id", d.ContainerID, "port_id", d.PortID, "stored_at", d.StoredAt)
}

type containerPresentedData struct {
	StorageJobID       string    `json:"storage_job_id"`
	AsrsID             string    `json:"asrs_id"`
	ContainerID        string    `json:"container_id"`
	PortID             string    `json:"port_id"`
	Reference          string    `json:"reference"`
	ScanResult         string    `json:"scan_result"`
	ScannedContainerID string    `json:"scanned_container_id"`
	PresentedAt        time.Time `json:"presented_at"`
}

func (d containerPresentedData) check() error {
	return errors.Join(required("storage_job_id", d.StorageJobID, "asrs_id", d.AsrsID, "container_id", d.ContainerID, "port_id", d.PortID,
		"presented_at", d.PresentedAt), oneOf("scan_result", d.ScanResult, "READ", "NO_READ", "MISMATCH"))
}

type storageJobFailedData struct {
	StorageJobID string    `json:"storage_job_id"`
	AsrsID       string    `json:"asrs_id"`
	JobKind      string    `json:"job_kind"`
	ContainerID  string    `json:"container_id"`
	PortID       string    `json:"port_id"`
	Reference    string    `json:"reference"`
	Reason       string    `json:"reason"`
	PLCReason    string    `json:"plc_reason"`
	FailedAt     time.Time `json:"failed_at"`
}

func (d storageJobFailedData) check() error {
	return errors.Join(required("storage_job_id", d.StorageJobID, "asrs_id", d.AsrsID, "container_id", d.ContainerID, "reason", d.Reason,
		"failed_at", d.FailedAt), oneOf("job_kind", d.JobKind, "STORE", "RETRIEVE"))
}

type equipmentStateData struct {
	AsrsID        string    `json:"asrs_id"`
	UnitID        string    `json:"unit_id"`
	Aisle         int       `json:"aisle"`
	State         string    `json:"state"`
	PreviousState string    `json:"previous_state"`
	ChangedAt     time.Time `json:"changed_at"`
}

func (d equipmentStateData) check() error {
	return required("asrs_id", d.AsrsID, "unit_id", d.UnitID, "state", d.State, "changed_at", d.ChangedAt)
}

type asrsCapacityData struct {
	AsrsID         string    `json:"asrs_id"`
	UnitsTotal     int       `json:"units_total"`
	UnitsAvailable int       `json:"units_available"`
	PositionsTotal int       `json:"positions_total"`
	PositionsFree  int       `json:"positions_free"`
	OpenJobs       int       `json:"open_jobs"`
	MeasuredAt     time.Time `json:"measured_at"`
}

func (d asrsCapacityData) check() error {
	return required("asrs_id", d.AsrsID, "measured_at", d.MeasuredAt)
}

// AsrsHandlers maps the five consumed asrs-control types to the use cases.
func AsrsHandlers(f Facts) map[string]HandlerFunc {
	return map[string]HandlerFunc{
		TypeContainerStored: handle(func(ctx context.Context, id, typ string, d containerStoredData) error {
			return f.OnContainerStored(ctx, id, typ, facts.ContainerStored{JobID: d.StorageJobID, AsrsID: d.AsrsID, ContainerID: d.ContainerID,
				PortID: d.PortID, Reference: d.Reference, At: d.StoredAt.UTC()})
		}),
		TypeContainerPresented: handle(func(ctx context.Context, id, typ string, d containerPresentedData) error {
			return f.OnContainerPresented(ctx, id, typ, facts.ContainerPresented{JobID: d.StorageJobID, AsrsID: d.AsrsID, ContainerID: d.ContainerID,
				PortID: d.PortID, Reference: d.Reference, ScanResult: d.ScanResult, ScannedContainerID: d.ScannedContainerID, At: d.PresentedAt.UTC()})
		}),
		TypeStorageJobFailed: handle(func(ctx context.Context, id, typ string, d storageJobFailedData) error {
			return f.OnStorageJobFailed(ctx, id, typ, facts.StorageJobFailed{JobID: d.StorageJobID, AsrsID: d.AsrsID, Kind: d.JobKind,
				ContainerID: d.ContainerID, PortID: d.PortID, Reference: d.Reference, Reason: d.Reason, PLCReason: d.PLCReason, At: d.FailedAt.UTC()})
		}),
		TypeEquipmentStateChanged: handle(func(ctx context.Context, id, typ string, d equipmentStateData) error {
			return f.OnEquipmentStateChanged(ctx, id, typ, facts.EquipmentState{AsrsID: d.AsrsID, UnitID: d.UnitID, Aisle: d.Aisle, State: d.State,
				PreviousState: d.PreviousState, At: d.ChangedAt.UTC()})
		}),
		TypeAsrsCapacityChanged: handle(func(ctx context.Context, id, typ string, d asrsCapacityData) error {
			return f.OnAsrsCapacityChanged(ctx, id, typ, facts.AsrsCapacity{AsrsID: d.AsrsID, UnitsTotal: d.UnitsTotal, UnitsAvailable: d.UnitsAvailable,
				PositionsTotal: d.PositionsTotal, PositionsFree: d.PositionsFree, OpenJobs: d.OpenJobs, At: d.MeasuredAt.UTC()})
		}),
	}
}
