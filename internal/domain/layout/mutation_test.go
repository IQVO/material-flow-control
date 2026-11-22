package layout_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/IQVO/material-flow-control/internal/domain/layout"
)

// carry (Task 2 review, Task 26 mutation work): the two rate floors are inclusive — a rate of exactly 1 is a valid
// layout (kills the < 1 -> <= 1 boundary mutants of legsPerRobotHour and retrievalsPerUnitHour).
func TestARateOfExactlyOneIsAccepted(t *testing.T) {
	s := floor()
	s.Fleet.LegsPerRobotHour = 1
	s.Asrs[0].RetrievalsPerUnitHour = 1
	l := mustNew(t, s)
	if l.Fleet().LegsPerRobotHour != 1 {
		t.Fatalf("fleet = %+v, want legsPerRobotHour 1", l.Fleet())
	}
}

// An AS/RS needs both port kinds: only OUTPUT ports is refused as only INPUT ports is.
func TestAnAsrsWithOnlyOutputPortsIsRefused(t *testing.T) {
	s := floor()
	s.Asrs[0].Ports = []layout.Port{{PortID: "OUT1", Kind: layout.PortOutput, LocationCode: "A", HandoffPointID: "H"}}
	if _, err := layout.New(s); !errors.Is(err, layout.ErrInvalid) || !strings.Contains(err.Error(), "needs at least one INPUT and one OUTPUT port") {
		t.Fatalf("New = %v, want the both-kinds refusal", err)
	}
}

// checkTrimmed names the field of the padded value, for the first and the last pair it is given.
func TestAPaddedValueIsNamedByItsOwnField(t *testing.T) {
	for _, c := range []struct {
		mutate func(s *layout.Spec)
		want   string
	}{
		{func(s *layout.Spec) { s.Asrs[0].Ports[0].PortID = " IN1" }, `portId " IN1" has surrounding whitespace`},
		{func(s *layout.Spec) { s.Asrs[0].Ports[0].HandoffPointID = "ASRS1-IN1 " }, `handoffPointId "ASRS1-IN1 " has surrounding whitespace`},
	} {
		s := floor()
		c.mutate(&s)
		if _, err := layout.New(s); !errors.Is(err, layout.ErrInvalid) || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("New = %v, want %q", err, c.want)
		}
	}
}
