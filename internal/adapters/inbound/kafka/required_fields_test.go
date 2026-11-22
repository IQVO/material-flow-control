package kafka

// carry (Task 15 review): every field the producer's schema requires is required by the consumer too (a message
// without it is poison and never reaches a use case), and the enums are checked. asrs-control's required lists are
// read from the pinned contract; amr-fleet-control's too since its pin landed (ruling P4-R9: the plan 03 table is
// swapped for the pinned yaml).

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	kafkago "github.com/segmentio/kafka-go"

	"github.com/IQVO/material-flow-control/internal/application/facts"
)

// asrsSchemas names the pinned schema of each consumed asrs-control type.
var asrsSchemas = map[string]string{TypeContainerStored: "ContainerStoredEvent", TypeContainerPresented: "ContainerPresentedAtPortEvent",
	TypeStorageJobFailed: "StorageJobFailedEvent", TypeEquipmentStateChanged: "EquipmentStateChangedEvent", TypeAsrsCapacityChanged: "AsrsCapacityChangedEvent"}

// requiredData lists the `data.required` of one pinned schema (inline or through its $ref, Task 24 D1).
func requiredData(t *testing.T, doc map[string]any, schema string) []string {
	t.Helper()
	req, ok := dataSchema(t, doc, schema)["required"].([]any)
	if !ok {
		t.Fatalf("schema %s has no data.required", schema)
	}
	out := make([]string, len(req))
	for i, r := range req {
		out[i], _ = r.(string)
	}
	return out
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

// amrSchemas names the pinned schema of each consumed amr-fleet-control type.
// ruling P4-R9 (Task 24): the required, optional and reason tables are read from the pinned contract, no longer
// copied from plan 03's Task 13 schema table.
var amrSchemas = map[string]string{TypeLegAssigned: "LegAssignedEvent", TypeContainerPickedUp: "ContainerPickedUpEvent",
	TypeLegCompleted: "LegCompletedEvent", TypeLegFailed: "LegFailedEvent", TypeFleetCapacityChanged: "FleetCapacityChangedEvent"}

func amrPin(t *testing.T) map[string]any {
	t.Helper()
	return pinned(t, "amr-fleet-control.asyncapi.yaml")
}

// optionalData lists the data properties of one pinned schema that its `required` does not name.
func optionalData(t *testing.T, doc map[string]any, schema string) []string {
	t.Helper()
	req := requiredData(t, doc, schema)
	var out []string
	for _, f := range dataFields(t, doc, schema) {
		if !slices.Contains(req, f) {
			out = append(out, f)
		}
	}
	return out
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

func TestEveryFieldThePinnedAmrSchemaRequiresIsRequired(t *testing.T) {
	doc := amrPin(t)
	checked := 0
	for _, p := range examples(doc) {
		typ, _ := p["type"].(string)
		schema, ok := amrSchemas[typ]
		if !ok {
			continue
		}
		for _, f := range requiredData(t, doc, schema) {
			assertPoison(t, AmrHandlers, withoutField(t, p, f), typ+" without "+f)
			checked++
		}
	}
	if checked < 37 {
		t.Fatalf("only %d required fields checked: the pinned examples or schemas were not found", checked)
	}
}

// The Task 16 fixtures stay usable beside the pin: each carries exactly fields the pinned schema declares, and every
// field it requires.
func TestEveryAmrFixtureSatisfiesThePinnedSchema(t *testing.T) {
	doc := amrPin(t)
	for typ, p := range amrFixtures(t) {
		schema, ok := amrSchemas[typ]
		if !ok {
			continue
		}
		data, _ := p["data"].(map[string]any)
		have := dataFields(t, doc, schema)
		for f := range data {
			if !slices.Contains(have, f) {
				t.Fatalf("the %s fixture carries %q, the pinned schema has no such field", typ, f)
			}
		}
		for _, f := range requiredData(t, doc, schema) {
			if _, ok := data[f]; !ok {
				t.Fatalf("the %s fixture lacks %q, which the pinned schema requires", typ, f)
			}
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

// carry (Task 16 review): every LegFailed reason of the pinned enum (ruling P4-R9, Task 24) is applied as is; any
// other is poison.
func TestEveryPinnedLegFailedReasonIsAppliedAndAnUnknownOneIsPoison(t *testing.T) {
	p := amrFixtures(t)[TypeLegFailed]
	reasons, _ := get(dataSchema(t, amrPin(t), "LegFailedEvent"), "properties", "reason", "enum").([]any)
	if len(reasons) < 8 {
		t.Fatalf("pinned LegFailed reasons = %v", reasons)
	}
	for _, v := range reasons {
		reason, _ := v.(string)
		r := &recorder{}
		err := testConsumer(AmrHandlers(r)).HandleMessage(context.Background(), withField(t, p, "reason", reason))
		if err != nil || r.count() != 1 || r.calls[0].(facts.LegFailed).Reason != reason {
			t.Fatalf("reason %s: err %v, calls %+v", reason, err, r.calls)
		}
	}
	assertPoison(t, AmrHandlers, withField(t, p, "reason", "EXPLODED"), "LegFailed reason EXPLODED")
}

// carry (Task 16 review): a fact without one of its optional fields is still applied, once.
func TestAnAmrFactWithoutAnOptionalFieldIsStillApplied(t *testing.T) {
	fixtures, doc := amrFixtures(t), amrPin(t)
	checked := 0
	for typ, schema := range amrSchemas {
		p, ok := fixtures[typ]
		if !ok {
			t.Fatalf("no fixture of %s", typ)
		}
		for _, f := range optionalData(t, doc, schema) {
			checked++
			if _, ok := p["data"].(map[string]any)[f]; !ok {
				t.Fatalf("the %s fixture has no %s: removing it would prove nothing", typ, f)
			}
			r := &recorder{}
			if err := testConsumer(AmrHandlers(r)).HandleMessage(context.Background(), withoutField(t, p, f)); err != nil || r.count() != 1 {
				t.Fatalf("%s without %s: err %v, applied %d (want 1)", typ, f, err, r.count())
			}
		}
	}
	if checked < 8 {
		t.Fatalf("only %d optional fields checked: the pinned schemas were not found", checked)
	}
}

// carry (Task 16 review): a fact without its robot names the missing robot once in the dead letter's x-dlq-error.
func TestAMissingRobotIsNamedOnceInTheDeadLetter(t *testing.T) {
	fixtures := amrFixtures(t)
	for _, typ := range []string{TypeLegAssigned, TypeContainerPickedUp, TypeLegCompleted} {
		value := withoutField(t, fixtures[typ], "robot")
		cause := testConsumer(AmrHandlers(&recorder{})).HandleMessage(context.Background(), value)
		if !errors.Is(cause, errPoison) {
			t.Fatalf("%s without robot: err = %v (want poison)", typ, cause)
		}
		w := &recordingWriter{}
		if err := publishDeadLetter(context.Background(), w, AmrTopic+DLQSuffix, kafkago.Message{Topic: AmrTopic, Value: value}, cause); err != nil {
			t.Fatal(err)
		}
		if got, _ := headerOf(w.written[0], "x-dlq-error"); strings.Count(got, "missing robot") != 1 {
			t.Fatalf("%s x-dlq-error = %q, want one \"missing robot\"", typ, got)
		}
	}
}
