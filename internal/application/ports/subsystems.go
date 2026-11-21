package ports

import (
	"context"

	"github.com/IQVO/material-flow-control/internal/application/subsystem"
)

// AsrsClient is asrs-control's subsystem-controller interface (relationship 5). Every POST carries key as its
// Idempotency-Key. Errors: *subsystem.Refusal for a problem answer, subsystem.ErrUnreachable (wrapped) otherwise.
// RecommendedInputPort answers "" when asrs-control has no available aisle.
type AsrsClient interface {
	CreateStorageJob(ctx context.Context, key, asrsID string, r subsystem.StorageJobRequest) (subsystem.Accepted, error)
	CancelStorageJob(ctx context.Context, key, asrsID, jobID string) (subsystem.Accepted, error)
	ConfirmPickup(ctx context.Context, key, asrsID, portID, containerID string) error
	RecommendedInputPort(ctx context.Context, asrsID string) (string, error)
}

// AmrClient is amr-fleet-control's subsystem-controller interface (relationship 4), same error contract.
type AmrClient interface {
	RequestLeg(ctx context.Context, key, fleetID string, r subsystem.LegRequest) (subsystem.Accepted, error)
	CancelLeg(ctx context.Context, key, fleetID, legID string) (subsystem.Accepted, error)
}
