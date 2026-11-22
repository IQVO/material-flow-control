package kafka

import (
	"slices"
	"testing"
)

// carry (Task 24 review): the LegFailed reasons we accept are EXACTLY the pinned enum. A reason the producer never
// sends (a superset) hides a contract drift; a missing one sends a real fact to the DLQ.
func TestTheLegFailedReasonsAreExactlyThePinnedEnum(t *testing.T) {
	enum, _ := get(dataSchema(t, pinned(t, "amr-fleet-control.asyncapi.yaml"), "LegFailedEvent"), "properties", "reason", "enum").([]any)
	var pinnedReasons []string
	for _, v := range enum {
		s, ok := v.(string)
		if !ok {
			t.Fatalf("LegFailed reason enum holds %v", v)
		}
		pinnedReasons = append(pinnedReasons, s)
	}
	if len(pinnedReasons) == 0 {
		t.Fatal("the pinned LegFailed has no reason enum")
	}
	slices.Sort(pinnedReasons)
	ours := slices.Sorted(slices.Values(legFailedReasons))
	if !slices.Equal(ours, pinnedReasons) {
		t.Fatalf("legFailedReasons = %v, the pinned enum = %v (each reason once)", ours, pinnedReasons)
	}
}
