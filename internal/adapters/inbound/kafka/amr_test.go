package kafka

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"

	"github.com/IQVO/material-flow-control/internal/application/facts"
)

func TestEveryAmrFixtureDecodesToItsExactValueAndOtherTypesAreSkipped(t *testing.T) {
	b, err := os.ReadFile("testdata/amr-fleet-control.examples.json")
	if err != nil {
		t.Fatal(err)
	}
	var evs []json.RawMessage
	if err := json.Unmarshal(b, &evs); err != nil {
		t.Fatal(err)
	}
	r := &recorder{}
	c := testConsumer(AmrHandlers(r))
	for _, ev := range evs {
		if err := c.HandleMessage(context.Background(), ev); err != nil {
			t.Fatalf("%s: %v", ev, err)
		}
	}
	want := []any{
		facts.LegAssigned{LegID: "leg-1", FleetID: "AMR1", ContainerID: "C-1", From: "ASRS1-OUT1", To: "ST1", Reference: "to-1.2.1", Robot: "EMU/R1", At: at},
		facts.ContainerPickedUp{LegID: "leg-1", FleetID: "AMR1", ContainerID: "C-1", From: "ASRS1-OUT1", Reference: "to-1.2.1", Robot: "EMU/R1", At: at},
		facts.LegCompleted{LegID: "leg-1", FleetID: "AMR1", ContainerID: "C-1", To: "ST1", Reference: "to-1.2.1", Robot: "EMU/R1", At: at},
		facts.LegFailed{LegID: "leg-2", FleetID: "AMR1", ContainerID: "C-2", From: "ST1", To: "ST2", Reference: "to-2.1.1", Robot: "EMU/R1",
			Reason: "PICK_FAILED", RobotError: "LOAD_NOT_AVAILABLE", At: at},
		facts.FleetCapacity{FleetID: "AMR1", RobotsTotal: 3, RobotsOnline: 3, RobotsAvailable: 2, RobotsCharging: 1, LegsQueued: 1, LegsActive: 1, At: at},
	}
	if !reflect.DeepEqual(r.calls, want) {
		t.Fatalf("calls =\n%+v\nwant\n%+v", r.calls, want)
	}
}

func TestALegFailedWithoutARobotKeepsAnEmptyRobotAndMalformedAmrFactsArePoison(t *testing.T) {
	r := &recorder{}
	c := testConsumer(AmrHandlers(r))
	err := c.HandleMessage(context.Background(), event("id", TypeLegFailed, map[string]any{"leg_id": "leg-3", "fleet_id": "AMR1", "container_id": "C",
		"from_handoff_point_id": "A", "to_handoff_point_id": "B", "reason": "ROBOT_OFFLINE", "container_on_robot": true, "failed_at": "2026-11-20T08:00:01Z"}))
	if err != nil || r.calls[0].(facts.LegFailed).Robot != "" || !r.calls[0].(facts.LegFailed).ContainerOnRobot {
		t.Fatalf("no robot = %v %+v", err, r.calls)
	}
	bad := []struct {
		typ  string
		data map[string]any
	}{
		{TypeLegAssigned, map[string]any{"leg_id": "leg-1", "fleet_id": "AMR1", "container_id": "C", "assigned_at": "2026-11-20T08:00:01Z"}},
		{TypeContainerPickedUp, map[string]any{"leg_id": "leg-1", "fleet_id": "AMR1", "container_id": "C", "from_handoff_point_id": "A", "picked_up_at": "2026-11-20T08:00:01Z"}},
		{TypeLegCompleted, map[string]any{"leg_id": "leg-1", "fleet_id": "AMR1", "container_id": "C", "robot": map[string]any{"manufacturer": "EMU", "serial_number": "R1"}, "completed_at": "2026-11-20T08:00:01Z"}},
		{TypeLegFailed, map[string]any{"leg_id": "leg-1", "fleet_id": "AMR1", "container_id": "C", "failed_at": "2026-11-20T08:00:01Z"}},
		{TypeFleetCapacityChanged, map[string]any{"robots_total": 1, "changed_at": "2026-11-20T08:00:01Z"}},
	}
	for _, tc := range bad {
		r := &recorder{}
		if err := testConsumer(AmrHandlers(r)).HandleMessage(context.Background(), event("id", tc.typ, tc.data)); !errors.Is(err, errPoison) || r.count() != 0 {
			t.Fatalf("%s: err = %v", tc.typ, err)
		}
	}
}

func TestAFleetOfZeroRobotsIsAValidCapacityFact(t *testing.T) {
	r := &recorder{}
	err := testConsumer(AmrHandlers(r)).HandleMessage(context.Background(), event("id", TypeFleetCapacityChanged, map[string]any{"fleet_id": "AMR1",
		"robots_total": 0, "robots_online": 0, "robots_available": 0, "robots_charging": 0, "robots_held": 0, "legs_queued": 0,
		"legs_active": 0, "changed_at": "2026-11-20T08:00:01Z"}))
	want := facts.FleetCapacity{FleetID: "AMR1", At: at}
	if err != nil || r.count() != 1 || !reflect.DeepEqual(r.calls[0], want) {
		t.Fatalf("robots_total 0 (plan 03 allows it, unlike asrs-control's units_total >= 1) = %v %+v", err, r.calls)
	}
}

func TestTheAmrReaderIsItsOwnFixedGroup(t *testing.T) {
	c := NewAmrConsumer([]string{"127.0.0.1:9092"}, "material-flow-control.amr-fleet-control-events", &recorder{}, quiet())
	defer func() { _ = c.Close() }()
	if c.Topic != "warehouse.amr-fleet-control.events" || len(c.Handlers) != 5 || c.DLQ == nil {
		t.Fatalf("consumer = %+v", c)
	}
}
