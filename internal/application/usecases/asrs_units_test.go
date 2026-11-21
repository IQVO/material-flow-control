package usecases_test

import (
	"context"
	"errors"
	"testing"

	"github.com/IQVO/material-flow-control/internal/application/usecases"
)

// carry (Task 20 review): an AS/RS the layout does not name is asrs-not-found, not an empty list that reads as
// "no unit reported yet"; a configured one still lists what was reported.
func TestEquipmentUnitsRefusesAnAsrsTheLayoutDoesNotName(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for _, id := range []string{"ASRS9", "", " ASRS1"} {
		if u, err := e.svc.EquipmentUnits(ctx, id); !errors.Is(err, usecases.ErrAsrsNotFound) || u != nil {
			t.Errorf("EquipmentUnits(%q) = %+v %v, want ErrAsrsNotFound", id, u, err)
		}
	}
	if usecases.ErrAsrsNotFound.Error() != "AS/RS not found" {
		t.Fatalf("message = %q", usecases.ErrAsrsNotFound)
	}
	if u, err := e.svc.EquipmentUnits(ctx, "ASRS1"); err != nil || len(u) != 0 {
		t.Fatalf("a configured AS/RS = %+v %v", u, err)
	}
}
