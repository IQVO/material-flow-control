package layout_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/IQVO/material-flow-control/internal/domain/layout"
)

// carry (Task 3 review): Resolve, ByHandoffPoint and Port compare exactly, so an id or location code configured
// with surrounding whitespace would never match a request or a fact. New refuses it, naming the entry and field;
// a value that is only whitespace stays "required" (TestABlankLookingIDIsMissing).
func TestAnIDWithSurroundingWhitespaceIsRefusedNamingTheEntry(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(s *layout.Spec)
		want   string
	}{
		{"site id", func(s *layout.Spec) { s.SiteID = " WH1" }, `layout: siteId " WH1" has surrounding whitespace`},
		{"fleet id", func(s *layout.Spec) { s.Fleet.FleetID = "AMR1 " }, `layout: fleet.fleetId "AMR1 " has surrounding whitespace`},
		{"asrs id", func(s *layout.Spec) { s.Asrs[0].AsrsID = "ASRS1\t" }, `asrs[0]: asrsId "ASRS1\t" has surrounding whitespace`},
		{"asrs location", func(s *layout.Spec) { s.Asrs[0].LocationCode = " WH1-AUTO-ASRS-ASRS1-00-00-S" },
			`asrs[0]: locationCode " WH1-AUTO-ASRS-ASRS1-00-00-S" has surrounding whitespace`},
		{"port id", func(s *layout.Spec) { s.Asrs[0].Ports[1].PortID = "OUT1 " }, `AS/RS ASRS1 ports[1]: portId "OUT1 " has surrounding whitespace`},
		{"port location", func(s *layout.Spec) { s.Asrs[0].Ports[2].LocationCode = "WH1-AUTO-ASRS-ASRS1-02-00-IN2\n" },
			`AS/RS ASRS1 ports[2]: locationCode "WH1-AUTO-ASRS-ASRS1-02-00-IN2\n" has surrounding whitespace`},
		{"port handoff", func(s *layout.Spec) { s.Asrs[0].Ports[0].HandoffPointID = " ASRS1-IN1" },
			`AS/RS ASRS1 ports[0]: handoffPointId " ASRS1-IN1" has surrounding whitespace`},
		{"station id", func(s *layout.Spec) { s.Stations[0].StationID = "ST1 " }, `stations[0]: stationId "ST1 " has surrounding whitespace`},
		{"station location", func(s *layout.Spec) { s.Stations[0].LocationCode = " WH1-GTP-ST1" },
			`stations[0]: locationCode " WH1-GTP-ST1" has surrounding whitespace`},
		{"station handoff", func(s *layout.Spec) { s.Stations[0].HandoffPointID = "ST1 " }, `stations[0]: handoffPointId "ST1 " has surrounding whitespace`},
		{"handoff location", func(s *layout.Spec) { s.Handoffs[2].LocationCode = "WH1-FWD-FWD1 " },
			`handoffs[2]: locationCode "WH1-FWD-FWD1 " has surrounding whitespace`},
		{"handoff point", func(s *layout.Spec) { s.Handoffs[1].HandoffPointID = " PK1" }, `handoffs[1]: handoffPointId " PK1" has surrounding whitespace`},
		{"clarification location", func(s *layout.Spec) { s.ClarificationPoints[0].LocationCode = " WH1-CLR-CLR1" },
			`clarificationPoints[0]: locationCode " WH1-CLR-CLR1" has surrounding whitespace`},
		{"clarification handoff", func(s *layout.Spec) { s.ClarificationPoints[0].HandoffPointID = "CLR1 " },
			`clarificationPoints[0]: handoffPointId "CLR1 " has surrounding whitespace`},
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
