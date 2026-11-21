package layout_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/IQVO/material-flow-control/internal/domain/layout"
)

// carry (Task 2 review): a Layout keeps no slice of the caller's Spec, so mutating the Spec after New changes
// nothing the layout answers.
func TestMutatingTheSpecAfterNewDoesNotChangeTheLayout(t *testing.T) {
	s := floor()
	l := mustNew(t, s)
	s.Asrs[0].Ports[0].PortID = "MUTATED"
	s.Asrs[0].Ports[0].LocationCode = "MUTATED"
	s.Stations[0].BufferLimit = 99
	s.Handoffs[0].Kind = layout.HandoffOther
	s.ClarificationPoints[0].LocationCode = "MUTATED"
	s.ClarificationPoints[0].HandoffPointID = "MUTATED"
	a, ok := l.Asrs("ASRS1")
	if !ok || len(a.Ports) != 4 || a.Ports[0].PortID != "IN1" || a.Ports[0].LocationCode != "WH1-AUTO-ASRS-ASRS1-01-00-IN1" {
		t.Fatalf("Asrs(ASRS1) = %+v %v after the Spec changed", a, ok)
	}
	if cp := l.ClarificationPoint(); cp.Kind != layout.PlaceClarification || cp.Code != "WH1-CLR-CLR1" || cp.HandoffPointID != "CLR1" {
		t.Fatalf("ClarificationPoint = %+v after the Spec changed", cp)
	}
	if st, ok := l.Station("ST1"); !ok || st.BufferLimit != 2 {
		t.Fatalf("Station(ST1) = %+v %v after the Spec changed", st, ok)
	}
	if p, ok := l.Resolve("WH1-RCV-DEC1"); !ok || p.HandoffKind != layout.HandoffDecant {
		t.Fatalf("Resolve(WH1-RCV-DEC1) = %+v %v after the Spec changed", p, ok)
	}
}

// carry (Task 2 review): Asrs hands out a copy of its ports, so mutating the returned slice changes nothing.
func TestMutatingTheReturnedPortsDoesNotChangeTheLayout(t *testing.T) {
	l := mustNew(t, floor())
	a, _ := l.Asrs("ASRS1")
	a.Ports[0].PortID = "MUTATED"
	a.Ports[1].Kind = layout.PortInput
	again, ok := l.Asrs("ASRS1")
	if !ok || again.Ports[0].PortID != "IN1" || again.Ports[1].Kind != layout.PortOutput {
		t.Fatalf("Asrs(ASRS1) = %+v %v after its returned Ports changed", again, ok)
	}
}

// carry (Task 2 review): an id that is only whitespace is missing, and the entry refusals name the entry.
func TestABlankLookingIDIsMissing(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(s *layout.Spec)
		want   string
	}{
		{"fleet id", func(s *layout.Spec) { s.Fleet.FleetID = " " }, "fleet.fleetId is required"},
		{"asrs id", func(s *layout.Spec) { s.Asrs[0].AsrsID = "\t" },
			`an AS/RS needs asrsId and locationCode (asrs[0], asrsId "\t", locationCode "WH1-AUTO-ASRS-ASRS1-00-00-S")`},
		{"asrs location", func(s *layout.Spec) { s.Asrs[0].LocationCode = " " },
			`an AS/RS needs asrsId and locationCode (asrs[0], asrsId "ASRS1", locationCode " ")`},
		{"port id", func(s *layout.Spec) { s.Asrs[0].Ports[1].PortID = " " },
			`AS/RS ASRS1: a port needs portId (ports[1], locationCode "WH1-AUTO-ASRS-ASRS1-01-00-OUT1")`},
		{"port location", func(s *layout.Spec) { s.Asrs[0].Ports[0].LocationCode = " " }, "a location code is required"},
		{"port handoff", func(s *layout.Spec) { s.Asrs[0].Ports[0].HandoffPointID = " " }, "handoff point id is required"},
		{"station id", func(s *layout.Spec) { s.Stations[0].StationID = " " },
			`a station needs stationId (stations[0], locationCode "WH1-GTP-ST1")`},
		{"station handoff", func(s *layout.Spec) { s.Stations[0].HandoffPointID = "\n" }, "handoff point id is required"},
		{"handoff location", func(s *layout.Spec) { s.Handoffs[1].LocationCode = " " }, "a location code is required"},
		{"handoff point", func(s *layout.Spec) { s.Handoffs[1].HandoffPointID = " " }, "handoff point id is required"},
		{"clarification location", func(s *layout.Spec) { s.ClarificationPoints[0].LocationCode = " " }, "a location code is required"},
		{"clarification handoff", func(s *layout.Spec) { s.ClarificationPoints[0].HandoffPointID = " " }, "handoff point id is required"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := floor()
			c.mutate(&s)
			_, err := layout.New(s)
			if !errors.Is(err, layout.ErrInvalid) || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("New = %v, want ErrInvalid containing %q", err, c.want)
			}
		})
	}
}

// carry (Task 2 review): a station's optional location code that is only whitespace is missing, not a reference.
func TestABlankLookingStationLocationCodeIsMissing(t *testing.T) {
	s := floor()
	s.Stations[0].LocationCode = " "
	l := mustNew(t, s)
	if p, ok := l.Resolve(" "); ok {
		t.Fatalf("Resolve(blank) = %+v, want no place", p)
	}
	if p, ok := l.Resolve("ST1"); !ok || p.Kind != layout.PlaceStation || p.StationID != "ST1" {
		t.Fatalf("Resolve(ST1) = %+v %v", p, ok)
	}
}
