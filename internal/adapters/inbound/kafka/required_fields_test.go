package kafka

// carry (Task 15 review): every field the producer's schema requires is required by the consumer too (a message
// without it is poison and never reaches a use case), and the enums are checked. asrs-control's required lists are
// read from the pinned contract; amr-fleet-control's are plan 03's Task 13 schema table (pinned in Task 24).

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"reflect"
	"testing"

	"github.com/IQVO/material-flow-control/internal/application/facts"
)

// asrsSchemas names the pinned schema of each consumed asrs-control type.
var asrsSchemas = map[string]string{TypeContainerStored: "ContainerStoredEvent", TypeContainerPresented: "ContainerPresentedAtPortEvent",
	TypeStorageJobFailed: "StorageJobFailedEvent", TypeEquipmentStateChanged: "EquipmentStateChangedEvent", TypeAsrsCapacityChanged: "AsrsCapacityChangedEvent"}

// requiredData lists the `data.required` of one pinned schema.
func requiredData(t *testing.T, doc map[string]any, schema string) []string {
	t.Helper()
	parts, _ := get(doc, "components", "schemas", schema, "allOf").([]any)
	for _, p := range parts {
		if req, ok := get(p, "properties", "data", "required").([]any); ok {
			out := make([]string, len(req))
			for i, r := range req {
				out[i], _ = r.(string)
			}
			return out
		}
	}
	t.Fatalf("schema %s has no data.required", schema)
	return nil
}

// withoutField re-encodes the CloudEvent example p with data field f removed.
func withoutField(t *testing.T, p map[string]any, f string) []byte {
	t.Helper()
	data := maps.Clone(p["data"].(map[string]any))
	delete(data, f)
	ev := maps.Clone(p)
	ev["data"] = data
	b, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// assertPoison fails unless value is dead-lettered as poison without reaching the use case.
func assertPoison(t *testing.T, handlers func(Facts) map[string]HandlerFunc, value []byte, what string) {
	t.Helper()
	r := &recorder{}
	if err := testConsumer(handlers(r)).HandleMessage(context.Background(), value); !errors.Is(err, errPoison) || r.count() != 0 {
		t.Fatalf("%s: err = %v, applied %d (want poison)", what, err, r.count())
	}
}

func TestEveryFieldThePinnedAsrsSchemaRequiresIsRequired(t *testing.T) {
	doc := pinned(t, "asrs-control.asyncapi.yaml")
	checked := 0
	for _, p := range examples(doc) {
		typ, _ := p["type"].(string)
		schema, ok := asrsSchemas[typ]
		if !ok {
			continue
		}
		for _, f := range requiredData(t, doc, schema) {
			assertPoison(t, AsrsHandlers, withoutField(t, p, f), typ+" without "+f)
			checked++
		}
	}
	if checked < 30 {
		t.Fatalf("only %d required fields checked: the pinned examples or schemas were not found", checked)
	}
}

func TestEquipmentStatesOutsideTheEnumArePoison(t *testing.T) {
	base := map[string]any{"asrs_id": "ASRS1", "unit_id": "A1", "aisle": 1, "state": "HELD", "previous_state": "IDLE", "changed_at": "2026-11-20T08:00:01Z"}
	for _, f := range []string{"state", "previous_state"} {
		data := maps.Clone(base)
		data[f] = "BROKEN"
		assertPoison(t, AsrsHandlers, event("id", TypeEquipmentStateChanged, data), f+" BROKEN")
	}
	r := &recorder{}
	if err := testConsumer(AsrsHandlers(r)).HandleMessage(context.Background(), event("id", TypeEquipmentStateChanged, base)); err != nil || r.count() != 1 {
		t.Fatalf("a valid state change: err %v, applied %d", err, r.count())
	}
}

// amrRequired is plan 03 Task 13's schema table: required = every field without `?`.
var amrRequired = map[string][]string{
	TypeLegAssigned:          {"leg_id", "fleet_id", "container_id", "from_handoff_point_id", "to_handoff_point_id", "robot", "map_version", "assigned_at"},
	TypeContainerPickedUp:    {"leg_id", "fleet_id", "container_id", "from_handoff_point_id", "robot", "picked_up_at"},
	TypeLegCompleted:         {"leg_id", "fleet_id", "container_id", "to_handoff_point_id", "robot", "completed_at"},
	TypeLegFailed:            {"leg_id", "fleet_id", "container_id", "from_handoff_point_id", "to_handoff_point_id", "reason", "container_on_robot", "failed_at"},
	TypeFleetCapacityChanged: {"fleet_id", "robots_total", "robots_online", "robots_available", "robots_charging", "robots_held", "legs_queued", "legs_active", "changed_at"},
}

func amrFixtures(t *testing.T) map[string]map[string]any {
	t.Helper()
	b, err := os.ReadFile("testdata/amr-fleet-control.examples.json")
	if err != nil {
		t.Fatal(err)
	}
	var evs []map[string]any
	if err := json.Unmarshal(b, &evs); err != nil {
		t.Fatal(err)
	}
	out := map[string]map[string]any{}
	for _, ev := range evs {
		out[ev["type"].(string)] = ev
	}
	return out
}

func TestEveryFieldPlan03RequiresOfAnAmrFactIsRequired(t *testing.T) {
	fixtures := amrFixtures(t)
	for typ, fields := range amrRequired {
		p, ok := fixtures[typ]
		if !ok {
			t.Fatalf("no fixture of %s", typ)
		}
		for _, f := range fields {
			assertPoison(t, AmrHandlers, withoutField(t, p, f), typ+" without "+f)
		}
	}
}

func TestAnIncompleteRobotIsPoisonEvenWhereTheRobotIsOptional(t *testing.T) {
	fixtures := amrFixtures(t)
	for _, typ := range []string{TypeLegAssigned, TypeContainerPickedUp, TypeLegCompleted, TypeLegFailed} {
		for _, f := range []string{"manufacturer", "serial_number"} {
			p := maps.Clone(fixtures[typ])
			data := maps.Clone(p["data"].(map[string]any))
			robot := maps.Clone(data["robot"].(map[string]any))
			delete(robot, f)
			data["robot"], p["data"] = robot, data
			b, err := json.Marshal(p)
			if err != nil {
				t.Fatal(err)
			}
			assertPoison(t, AmrHandlers, b, typ+" robot without "+f)
		}
	}
}

func TestANegativeFleetCountReachesTheUseCaseForTheClamp(t *testing.T) {
	r := &recorder{}
	err := testConsumer(AmrHandlers(r)).HandleMessage(context.Background(), event("id", TypeFleetCapacityChanged, map[string]any{"fleet_id": "AMR1",
		"robots_total": -1, "robots_online": 0, "robots_available": -2, "robots_charging": 0, "robots_held": 0, "legs_queued": -3,
		"legs_active": 0, "changed_at": "2026-11-20T08:00:01Z"}))
	want := facts.FleetCapacity{FleetID: "AMR1", RobotsTotal: -1, RobotsAvailable: -2, LegsQueued: -3, At: at}
	if err != nil || r.count() != 1 || !reflect.DeepEqual(r.calls[0], want) {
		t.Fatalf("err %v, calls %+v, want %+v", err, r.calls, want)
	}
}

// ruling P4-R9: map_version is a string in plan 03 (the producer); null, empty or a number is poison.
func TestAMapVersionThatIsNotANonEmptyStringIsPoison(t *testing.T) {
	p := amrFixtures(t)[TypeLegAssigned]
	if got := p["data"].(map[string]any)["map_version"]; got != "v1" {
		t.Fatalf("fixture map_version = %#v, want \"v1\" (ruling P4-R9)", got)
	}
	for _, tc := range []struct {
		name string
		v    any
	}{{"null", nil}, {"empty", ""}, {"an integer", 1}} {
		assertPoison(t, AmrHandlers, withField(t, p, "map_version", tc.v), "LegAssigned with map_version "+tc.name)
	}
}

// withField re-encodes the CloudEvent example p with data field f set to v.
func withField(t *testing.T, p map[string]any, f string, v any) []byte {
	t.Helper()
	data := maps.Clone(p["data"].(map[string]any))
	data[f] = v
	ev := maps.Clone(p)
	ev["data"] = data
	b, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// carry (Task 16 review): every LegFailed reason of plan 03's Task 13 table is applied as is; any other is poison.
func TestEveryPlan03LegFailedReasonIsAppliedAndAnUnknownOneIsPoison(t *testing.T) {
	p := amrFixtures(t)[TypeLegFailed]
	for _, reason := range []string{"CANCELLED", "ROBOT_BLOCKED", "ROBOT_FAULT", "ROBOT_OFFLINE", "ORDER_REJECTED", "PICK_FAILED", "DROP_FAILED",
		"MAP_VERSION_RETIRED"} {
		r := &recorder{}
		err := testConsumer(AmrHandlers(r)).HandleMessage(context.Background(), withField(t, p, "reason", reason))
		if err != nil || r.count() != 1 || r.calls[0].(facts.LegFailed).Reason != reason {
			t.Fatalf("reason %s: err %v, calls %+v", reason, err, r.calls)
		}
	}
	assertPoison(t, AmrHandlers, withField(t, p, "reason", "EXPLODED"), "LegFailed reason EXPLODED")
}
