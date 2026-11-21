package kafka

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/IQVO/material-flow-control/internal/application/facts"
)

// The amr-fleet-control payloads (plan 03's relationship-4 event fields; pinned in Task 24). robot_timestamp and
// map_version are ignored (tolerant reader).
//
// carry (Task 15 review): every field plan 03's Task 13 schema table requires is required here (map_version too:
// a string in plan 03, an integer in this fixture: only its presence is required), a present robot needs both of its fields, and counts are *int so an
// absent one is poison while a negative one still reaches the capacity clamp (D17).

type robotRef struct {
	Manufacturer string `json:"manufacturer"`
	SerialNumber string `json:"serial_number"`
}

// check fails a robot that is absent when required, or present without its manufacturer or serial number.
func (r *robotRef) check(isRequired bool) error {
	if r == nil {
		if isRequired {
			return required("robot", "")
		}
		return nil
	}
	return required("robot.manufacturer", r.Manufacturer, "robot.serial_number", r.SerialNumber)
}

func (r *robotRef) id() string {
	if r == nil || r.Manufacturer == "" {
		return ""
	}
	return r.Manufacturer + "/" + r.SerialNumber
}

type legAssignedData struct {
	LegID       string          `json:"leg_id"`
	FleetID     string          `json:"fleet_id"`
	ContainerID string          `json:"container_id"`
	From        string          `json:"from_handoff_point_id"`
	To          string          `json:"to_handoff_point_id"`
	Reference   string          `json:"reference"`
	Robot       *robotRef       `json:"robot"`
	MapVersion  json.RawMessage `json:"map_version"`
	AssignedAt  time.Time       `json:"assigned_at"`
}

func (d legAssignedData) check() error {
	return errors.Join(required("leg_id", d.LegID, "fleet_id", d.FleetID, "container_id", d.ContainerID, "robot", d.Robot.id(), "assigned_at", d.AssignedAt,
		"from_handoff_point_id", d.From, "to_handoff_point_id", d.To, "map_version", d.MapVersion), d.Robot.check(true))
}

type containerPickedUpData struct {
	LegID       string    `json:"leg_id"`
	FleetID     string    `json:"fleet_id"`
	ContainerID string    `json:"container_id"`
	From        string    `json:"from_handoff_point_id"`
	Reference   string    `json:"reference"`
	Robot       *robotRef `json:"robot"`
	PickedUpAt  time.Time `json:"picked_up_at"`
}

func (d containerPickedUpData) check() error {
	return errors.Join(required("leg_id", d.LegID, "fleet_id", d.FleetID, "container_id", d.ContainerID, "from_handoff_point_id", d.From,
		"robot", d.Robot.id(), "picked_up_at", d.PickedUpAt), d.Robot.check(true))
}

type legCompletedData struct {
	LegID       string    `json:"leg_id"`
	FleetID     string    `json:"fleet_id"`
	ContainerID string    `json:"container_id"`
	To          string    `json:"to_handoff_point_id"`
	Reference   string    `json:"reference"`
	Robot       *robotRef `json:"robot"`
	CompletedAt time.Time `json:"completed_at"`
}

func (d legCompletedData) check() error {
	return errors.Join(required("leg_id", d.LegID, "fleet_id", d.FleetID, "container_id", d.ContainerID, "to_handoff_point_id", d.To, "completed_at", d.CompletedAt),
		d.Robot.check(true))
}

type legFailedData struct {
	LegID            string    `json:"leg_id"`
	FleetID          string    `json:"fleet_id"`
	ContainerID      string    `json:"container_id"`
	From             string    `json:"from_handoff_point_id"`
	To               string    `json:"to_handoff_point_id"`
	Reference        string    `json:"reference"`
	Robot            *robotRef `json:"robot"`
	Reason           string    `json:"reason"`
	RobotError       string    `json:"robot_error"`
	ContainerOnRobot *bool     `json:"container_on_robot"`
	FailedAt         time.Time `json:"failed_at"`
}

func (d legFailedData) check() error {
	return errors.Join(required("leg_id", d.LegID, "fleet_id", d.FleetID, "container_id", d.ContainerID, "failed_at", d.FailedAt,
		"from_handoff_point_id", d.From, "to_handoff_point_id", d.To, "container_on_robot", d.ContainerOnRobot), d.Robot.check(false),
		oneOf("reason", d.Reason, "CANCELLED", "ROBOT_BLOCKED", "ROBOT_FAULT", "ROBOT_OFFLINE", "ORDER_REJECTED", "PICK_FAILED", "DROP_FAILED", "MAP_VERSION_RETIRED"))
}

type fleetCapacityData struct {
	FleetID         string    `json:"fleet_id"`
	RobotsTotal     *int      `json:"robots_total"`
	RobotsOnline    *int      `json:"robots_online"`
	RobotsAvailable *int      `json:"robots_available"`
	RobotsCharging  *int      `json:"robots_charging"`
	RobotsHeld      *int      `json:"robots_held"`
	LegsQueued      *int      `json:"legs_queued"`
	LegsActive      *int      `json:"legs_active"`
	ChangedAt       time.Time `json:"changed_at"`
}

func (d fleetCapacityData) check() error {
	return required("fleet_id", d.FleetID, "changed_at", d.ChangedAt, "robots_total", d.RobotsTotal, "robots_online", d.RobotsOnline,
		"robots_available", d.RobotsAvailable, "robots_charging", d.RobotsCharging, "robots_held", d.RobotsHeld, "legs_queued", d.LegsQueued,
		"legs_active", d.LegsActive)
}

// AmrHandlers maps the five consumed amr-fleet-control types to the use cases.
func AmrHandlers(f Facts) map[string]HandlerFunc {
	return map[string]HandlerFunc{
		TypeLegAssigned: handle(func(ctx context.Context, id, typ string, d legAssignedData) error {
			return f.OnLegAssigned(ctx, id, typ, facts.LegAssigned{LegID: d.LegID, FleetID: d.FleetID, ContainerID: d.ContainerID, From: d.From,
				To: d.To, Reference: d.Reference, Robot: d.Robot.id(), At: d.AssignedAt.UTC()})
		}),
		TypeContainerPickedUp: handle(func(ctx context.Context, id, typ string, d containerPickedUpData) error {
			return f.OnContainerPickedUp(ctx, id, typ, facts.ContainerPickedUp{LegID: d.LegID, FleetID: d.FleetID, ContainerID: d.ContainerID,
				From: d.From, Reference: d.Reference, Robot: d.Robot.id(), At: d.PickedUpAt.UTC()})
		}),
		TypeLegCompleted: handle(func(ctx context.Context, id, typ string, d legCompletedData) error {
			return f.OnLegCompleted(ctx, id, typ, facts.LegCompleted{LegID: d.LegID, FleetID: d.FleetID, ContainerID: d.ContainerID, To: d.To,
				Reference: d.Reference, Robot: d.Robot.id(), At: d.CompletedAt.UTC()})
		}),
		TypeLegFailed: handle(func(ctx context.Context, id, typ string, d legFailedData) error {
			return f.OnLegFailed(ctx, id, typ, facts.LegFailed{LegID: d.LegID, FleetID: d.FleetID, ContainerID: d.ContainerID, From: d.From, To: d.To,
				Reference: d.Reference, Robot: d.Robot.id(), Reason: d.Reason, RobotError: d.RobotError, ContainerOnRobot: *d.ContainerOnRobot, At: d.FailedAt.UTC()})
		}),
		TypeFleetCapacityChanged: handle(func(ctx context.Context, id, typ string, d fleetCapacityData) error {
			return f.OnFleetCapacityChanged(ctx, id, typ, facts.FleetCapacity{FleetID: d.FleetID, RobotsTotal: *d.RobotsTotal, RobotsOnline: *d.RobotsOnline,
				RobotsAvailable: *d.RobotsAvailable, RobotsCharging: *d.RobotsCharging, RobotsHeld: *d.RobotsHeld, LegsQueued: *d.LegsQueued,
				LegsActive: *d.LegsActive, At: d.ChangedAt.UTC()})
		}),
	}
}
