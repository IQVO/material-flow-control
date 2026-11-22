package usecases_test

import (
	"testing"

	"github.com/IQVO/material-flow-control/internal/application/facts"
	"github.com/IQVO/material-flow-control/internal/domain/capacity"
)

// Task 7 review (decision D17): negative counts from asrs-control clamp to 0 in the published snapshot, which is
// then UNAVAILABLE; the consumer passes them through undecided (kafka's
// TestANegativeAsrsCountReachesTheUseCaseAndAnOverflowingOneIsPoison).
func TestANegativeAsrsCountIsClampedInTheSnapshot(t *testing.T) {
	e := newEnv(t)
	e.must(e.svc.OnAsrsCapacityChanged(ctx, e.id(), typeAsrsCap, facts.AsrsCapacity{AsrsID: "ASRS1", UnitsTotal: -1, UnitsAvailable: -1, OpenJobs: -3}))
	all := e.events.All()
	last, ok := all[len(all)-1].(capacity.SubsystemCapacityChanged)
	if !ok || last.UnitsTotal != 0 || last.UnitsAvailable != 0 || last.OpenWork != 0 || last.CapacityPerHour != 0 ||
		last.Status != capacity.StatusUnavailable {
		t.Fatalf("snapshot = %+v, want every count 0 and UNAVAILABLE", all[len(all)-1])
	}
}
