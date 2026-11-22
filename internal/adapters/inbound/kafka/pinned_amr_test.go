package kafka

import (
	"slices"
	"testing"
)

func TestEveryPinnedAmrExampleOfAConsumedTypeIsApplied(t *testing.T) {
	seen := replay(t, pinned(t, "amr-fleet-control.asyncapi.yaml"), AmrHandlers(&recorder{}))
	for _, typ := range []string{TypeLegAssigned, TypeContainerPickedUp, TypeLegCompleted, TypeLegFailed, TypeFleetCapacityChanged} {
		if seen[typ] == 0 {
			t.Fatalf("the pinned contract has no example of %s", typ)
		}
	}
}

func TestEveryAmrFieldWeDecodeExistsInThePinnedSchema(t *testing.T) {
	doc := pinned(t, "amr-fleet-control.asyncapi.yaml")
	cases := map[string]any{"LegAssignedEvent": legAssignedData{}, "ContainerPickedUpEvent": containerPickedUpData{},
		"LegCompletedEvent": legCompletedData{}, "LegFailedEvent": legFailedData{}, "FleetCapacityChangedEvent": fleetCapacityData{}}
	for schema, payload := range cases {
		have := dataFields(t, doc, schema)
		for _, f := range jsonFields(payload) {
			if !slices.Contains(have, f) {
				t.Fatalf("%s: we decode %q, the producer's schema has no such field", schema, f)
			}
		}
	}
}

// The D17 / ADR 0006 mapping rests on these producer fields; a rename on either side fails here first.
func TestTheCapacityMappingFieldsExistInBothPinnedContracts(t *testing.T) {
	fleet := dataFields(t, pinned(t, "amr-fleet-control.asyncapi.yaml"), "FleetCapacityChangedEvent")
	for _, f := range []string{"fleet_id", "robots_total", "robots_online", "robots_available", "legs_queued", "legs_active", "changed_at"} {
		if !slices.Contains(fleet, f) {
			t.Fatalf("FleetCapacityChanged has no %s", f)
		}
	}
	asrs := dataFields(t, pinned(t, "asrs-control.asyncapi.yaml"), "AsrsCapacityChangedEvent")
	for _, f := range []string{"asrs_id", "units_total", "units_available", "open_jobs", "measured_at"} {
		if !slices.Contains(asrs, f) {
			t.Fatalf("AsrsCapacityChanged has no %s", f)
		}
	}
	// deviation (Task 24 D1): the pinned event references its data schema ($ref), so the minimum is read through dataSchema.
	minimum := -1
	if v, ok := get(dataSchema(t, pinned(t, "amr-fleet-control.asyncapi.yaml"), "FleetCapacityChangedEvent"), "properties", "robots_total", "minimum").(int); ok {
		minimum = v
	}
	if minimum != 0 {
		t.Fatalf("robots_total minimum = %d, want 0 (ADR 0006: a fleet of zero robots is a valid fact, unlike units_total >= 1)", minimum)
	}
}
