package kafka

import (
	"context"
	"log/slog"

	"github.com/IQVO/material-flow-control/internal/application/facts"
)

// The consumed topics: only the integration topics, never the .analytics twins.
const (
	AsrsTopic = "warehouse.asrs-control.events"
	AmrTopic  = "warehouse.amr-fleet-control.events"
)

// The ten consumed CloudEvents types (Global Constraints). Every other type on the two topics is committed past.
const (
	TypeContainerStored       = "com.warehouse.wes.asrs-control.storagejob.ContainerStored"
	TypeContainerPresented    = "com.warehouse.wes.asrs-control.storagejob.ContainerPresentedAtPort"
	TypeStorageJobFailed      = "com.warehouse.wes.asrs-control.storagejob.StorageJobFailed"
	TypeEquipmentStateChanged = "com.warehouse.wes.asrs-control.equipmentunit.EquipmentStateChanged"
	TypeAsrsCapacityChanged   = "com.warehouse.wes.asrs-control.asrs.AsrsCapacityChanged"
	TypeLegAssigned           = "com.warehouse.wes.amr-fleet-control.mission.LegAssigned"
	TypeContainerPickedUp     = "com.warehouse.wes.amr-fleet-control.mission.ContainerPickedUp"
	TypeLegCompleted          = "com.warehouse.wes.amr-fleet-control.mission.LegCompleted"
	TypeLegFailed             = "com.warehouse.wes.amr-fleet-control.mission.LegFailed"
	TypeFleetCapacityChanged  = "com.warehouse.wes.amr-fleet-control.fleet.FleetCapacityChanged"
)

// Facts is the use-case side of the consumers (usecases.Service). Each method claims the CloudEvents id in the
// inbox and applies the fact in ONE unit of work; nil = applied or deterministically skipped, non-nil = transient.
type Facts interface {
	OnContainerStored(ctx context.Context, eventID, eventType string, f facts.ContainerStored) error
	OnContainerPresented(ctx context.Context, eventID, eventType string, f facts.ContainerPresented) error
	OnStorageJobFailed(ctx context.Context, eventID, eventType string, f facts.StorageJobFailed) error
	OnEquipmentStateChanged(ctx context.Context, eventID, eventType string, f facts.EquipmentState) error
	OnAsrsCapacityChanged(ctx context.Context, eventID, eventType string, f facts.AsrsCapacity) error
	OnLegAssigned(ctx context.Context, eventID, eventType string, f facts.LegAssigned) error
	OnContainerPickedUp(ctx context.Context, eventID, eventType string, f facts.ContainerPickedUp) error
	OnLegCompleted(ctx context.Context, eventID, eventType string, f facts.LegCompleted) error
	OnLegFailed(ctx context.Context, eventID, eventType string, f facts.LegFailed) error
	OnFleetCapacityChanged(ctx context.Context, eventID, eventType string, f facts.FleetCapacity) error
}

// NewAsrsConsumer reads warehouse.asrs-control.events under groupID (MFC_ASRS_CONSUMER_GROUP).
func NewAsrsConsumer(brokers []string, groupID string, f Facts, logger *slog.Logger) *Consumer {
	return newConsumer("asrs-control-events", brokers, AsrsTopic, groupID, AsrsHandlers(f), logger)
}

// rebind rebuilds handlers for another Facts (the pinned-example tests use a fresh recorder per example).
func rebind(handlers map[string]HandlerFunc, f Facts) map[string]HandlerFunc {
	all := AsrsHandlers(f)
	for typ, h := range AmrHandlers(f) {
		all[typ] = h
	}
	out := make(map[string]HandlerFunc, len(handlers))
	for typ := range handlers {
		out[typ] = all[typ]
	}
	return out
}

// AmrHandlers is filled by Task 16.
func AmrHandlers(Facts) map[string]HandlerFunc { return map[string]HandlerFunc{} }
