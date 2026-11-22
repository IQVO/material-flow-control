package kafka

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/IQVO/material-flow-control/internal/application/facts"
)

// A negative count in AsrsCapacityChanged is not poison: the consumer hands it to the use case unchanged and the
// capacity snapshot clamps it to 0 (Task 7 review, decision D17; see usecases' TestANegativeAsrsCountIsClampedInTheSnapshot).
// A count no int can hold can never be decoded, so it is poison and dead-lettered at once.
func TestANegativeAsrsCountReachesTheUseCaseAndAnOverflowingOneIsPoison(t *testing.T) {
	r := &recorder{}
	c := testConsumer(AsrsHandlers(r))
	err := c.HandleMessage(context.Background(), event("id-1", TypeAsrsCapacityChanged, map[string]any{"asrs_id": "ASRS1", "units_total": -1,
		"units_available": -1, "positions_total": 0, "positions_free": 0, "open_jobs": -3, "measured_at": "2026-11-20T08:00:01Z"}))
	want := facts.AsrsCapacity{AsrsID: "ASRS1", UnitsTotal: -1, UnitsAvailable: -1, OpenJobs: -3, At: at}
	if err != nil || r.count() != 1 || !reflect.DeepEqual(r.calls[0], want) {
		t.Fatalf("err %v, calls %+v, want %+v", err, r.calls, want)
	}
	err = c.HandleMessage(context.Background(), event("id-2", TypeAsrsCapacityChanged, map[string]any{"asrs_id": "ASRS1", "units_total": 1e30,
		"units_available": 1, "open_jobs": 0, "measured_at": "2026-11-20T08:00:01Z"}))
	if !errors.Is(err, errPoison) || r.count() != 1 {
		t.Fatalf("overflowing units_total: err %v, calls %d", err, r.count())
	}
}
