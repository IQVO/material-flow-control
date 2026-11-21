package layout_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/IQVO/material-flow-control/internal/domain/layout"
)

// floor is the example floor of config/flow-layout.example.yaml, built in code.
func floor() layout.Spec {
	return layout.Spec{
		SiteID: "WH1",
		Fleet:  layout.Fleet{FleetID: "AMR1", LegsPerRobotHour: 30},
		Asrs: []layout.Asrs{{
			AsrsID: "ASRS1", LocationCode: "WH1-AUTO-ASRS-ASRS1-00-00-S", RetrievalsPerUnitHour: 60,
			Ports: []layout.Port{
				{PortID: "IN1", Kind: layout.PortInput, LocationCode: "WH1-AUTO-ASRS-ASRS1-01-00-IN1", HandoffPointID: "ASRS1-IN1"},
				{PortID: "OUT1", Kind: layout.PortOutput, LocationCode: "WH1-AUTO-ASRS-ASRS1-01-00-OUT1", HandoffPointID: "ASRS1-OUT1"},
				{PortID: "IN2", Kind: layout.PortInput, LocationCode: "WH1-AUTO-ASRS-ASRS1-02-00-IN2", HandoffPointID: "ASRS1-IN2"},
				{PortID: "OUT2", Kind: layout.PortOutput, LocationCode: "WH1-AUTO-ASRS-ASRS1-02-00-OUT2", HandoffPointID: "ASRS1-OUT2"},
			},
		}},
		Stations: []layout.Station{{StationID: "ST1", LocationCode: "WH1-GTP-ST1", HandoffPointID: "ST1", BufferLimit: 2}},
		Handoffs: []layout.Handoff{
			{LocationCode: "WH1-RCV-DEC1", HandoffPointID: "ST2", Kind: layout.HandoffDecant},
			{LocationCode: "WH1-PCK-PK1", HandoffPointID: "PK1", Kind: layout.HandoffPack},
			{LocationCode: "WH1-FWD-FWD1", HandoffPointID: "FWD1", Kind: layout.HandoffForward},
		},
		ClarificationPoints: []layout.ClarificationPoint{{LocationCode: "WH1-CLR-CLR1", HandoffPointID: "CLR1"}},
	}
}

func mustNew(t *testing.T, s layout.Spec) *layout.Layout {
	t.Helper()
	l, err := layout.New(s)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestEveryReferenceResolvesToItsCanonicalPlace(t *testing.T) {
	l := mustNew(t, floor())
	cases := []struct {
		ref  string
		want layout.Place
	}{
		{"WH1-AUTO-ASRS-ASRS1-00-00-S", layout.Place{Kind: layout.PlaceAsrs, Code: "WH1-AUTO-ASRS-ASRS1-00-00-S", AsrsID: "ASRS1"}},
		{"WH1-AUTO-ASRS-ASRS1-01-00-OUT1", layout.Place{Kind: layout.PlacePort, Code: "WH1-AUTO-ASRS-ASRS1-01-00-OUT1", AsrsID: "ASRS1", PortID: "OUT1", PortKind: layout.PortOutput, HandoffPointID: "ASRS1-OUT1"}},
		{"ST1", layout.Place{Kind: layout.PlaceStation, Code: "ST1", StationID: "ST1", HandoffPointID: "ST1"}},
		{"WH1-GTP-ST1", layout.Place{Kind: layout.PlaceStation, Code: "ST1", StationID: "ST1", HandoffPointID: "ST1"}},
		{"WH1-PCK-PK1", layout.Place{Kind: layout.PlaceHandoff, Code: "WH1-PCK-PK1", HandoffPointID: "PK1", HandoffKind: layout.HandoffPack}},
		{"WH1-CLR-CLR1", layout.Place{Kind: layout.PlaceClarification, Code: "WH1-CLR-CLR1", HandoffPointID: "CLR1"}},
	}
	for _, c := range cases {
		got, ok := l.Resolve(c.ref)
		if !ok || got != c.want {
			t.Fatalf("Resolve(%q) = %+v, %v; want %+v", c.ref, got, ok, c.want)
		}
	}
	if _, ok := l.Resolve("WH1-NOWHERE"); ok {
		t.Fatal("an unknown reference resolved")
	}
	if _, ok := l.Resolve(""); ok {
		t.Fatal("the empty reference resolved")
	}
}

// TestHandoffPointsPortsAndTheAsrsPlace is the plan 04 Task 2 brief's test split for gocognit (max 20) and cyclop
// (max 15): its assertions moved verbatim, in order, into assertHandoffPoints, assertPortsAndTheAsrsPlace, assertListings, assertClarificationHeaderAndZero;
// every assertion is byte-identical to the brief.
func TestHandoffPointsPortsAndTheAsrsPlace(t *testing.T) {
	l := mustNew(t, floor())
	assertHandoffPoints(t, l)
	assertPortsAndTheAsrsPlace(t, l)
	assertListings(t, l)
	assertClarificationHeaderAndZero(t, l)
}

func assertHandoffPoints(t *testing.T, l *layout.Layout) {
	t.Helper()
	if p, ok := l.ByHandoffPoint("ASRS1-IN2"); !ok || p.Kind != layout.PlacePort || p.PortID != "IN2" || p.PortKind != layout.PortInput {
		t.Fatalf("ByHandoffPoint(ASRS1-IN2) = %+v %v", p, ok)
	}
	if p, ok := l.ByHandoffPoint("ST2"); !ok || p.Kind != layout.PlaceHandoff || p.Code != "WH1-RCV-DEC1" || p.HandoffKind != layout.HandoffDecant {
		t.Fatalf("ByHandoffPoint(ST2) = %+v %v", p, ok)
	}
}

func assertPortsAndTheAsrsPlace(t *testing.T, l *layout.Layout) {
	t.Helper()
	if p, ok := l.Port("ASRS1", "OUT2"); !ok || p.Code != "WH1-AUTO-ASRS-ASRS1-02-00-OUT2" || p.HandoffPointID != "ASRS1-OUT2" {
		t.Fatalf("Port(ASRS1, OUT2) = %+v %v", p, ok)
	}
	if _, ok := l.Port("ASRS1", "OUT9"); ok {
		t.Fatal("an unknown port resolved")
	}
	if p, ok := l.AsrsPlace("ASRS1"); !ok || p.Code != "WH1-AUTO-ASRS-ASRS1-00-00-S" || p.AMR() {
		t.Fatalf("AsrsPlace = %+v %v (the AS/RS itself is no AMR place)", p, ok)
	}
}

func assertListings(t *testing.T, l *layout.Layout) {
	t.Helper()
	in := l.InputPorts("ASRS1")
	if len(in) != 2 || in[0].PortID != "IN1" || in[1].PortID != "IN2" {
		t.Fatalf("InputPorts = %+v", in)
	}
	if got := l.AsrsIDs(); len(got) != 1 || got[0] != "ASRS1" {
		t.Fatalf("AsrsIDs = %v", got)
	}
	if st, ok := l.Station("ST1"); !ok || st.BufferLimit != 2 {
		t.Fatalf("Station = %+v %v", st, ok)
	}
}

func assertClarificationHeaderAndZero(t *testing.T, l *layout.Layout) {
	t.Helper()
	if c := l.ClarificationPoint(); c.Code != "WH1-CLR-CLR1" || c.HandoffPointID != "CLR1" || !c.AMR() {
		t.Fatalf("ClarificationPoint = %+v", c)
	}
	if l.SiteID() != "WH1" || l.Fleet().FleetID != "AMR1" || l.Fleet().LegsPerRobotHour != 30 {
		t.Fatalf("header = %s %+v", l.SiteID(), l.Fleet())
	}
	if (layout.Place{}).IsZero() != true || (layout.Place{Kind: layout.PlaceAsrs}).IsZero() {
		t.Fatal("IsZero")
	}
}

func TestAnInvalidLayoutIsRefusedWithItsReason(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(s *layout.Spec)
		want   string
	}{
		{"no site", func(s *layout.Spec) { s.SiteID = " " }, "siteId is required"},
		{"no fleet", func(s *layout.Spec) { s.Fleet.FleetID = "" }, "fleet.fleetId is required"},
		{"no fleet rate", func(s *layout.Spec) { s.Fleet.LegsPerRobotHour = 0 }, "legsPerRobotHour must be at least 1"},
		{"no asrs", func(s *layout.Spec) { s.Asrs = nil }, "at least one AS/RS is required"},
		{"no clarification", func(s *layout.Spec) { s.ClarificationPoints = nil }, "at least one clarification point is required"},
		{"asrs rate", func(s *layout.Spec) { s.Asrs[0].RetrievalsPerUnitHour = 0 }, "retrievalsPerUnitHour must be at least 1"},
		{"asrs id", func(s *layout.Spec) { s.Asrs[0].AsrsID = "" }, "an AS/RS needs asrsId and locationCode"},
		{"port kind", func(s *layout.Spec) { s.Asrs[0].Ports[0].Kind = "SIDE" }, "kind must be INPUT or OUTPUT"},
		{"port id", func(s *layout.Spec) { s.Asrs[0].Ports[0].PortID = "" }, "a port needs portId"},
		{"duplicate port", func(s *layout.Spec) { s.Asrs[0].Ports[2].PortID = "IN1" }, "duplicate port ASRS1/IN1"},
		{"only inputs", func(s *layout.Spec) {
			s.Asrs[0].Ports = []layout.Port{{PortID: "IN1", Kind: layout.PortInput, LocationCode: "A", HandoffPointID: "H"}}
		}, "needs at least one INPUT and one OUTPUT port"},
		{"station buffer", func(s *layout.Spec) { s.Stations[0].BufferLimit = 0 }, "bufferLimit must be at least 1"},
		{"station handoff", func(s *layout.Spec) { s.Stations[0].HandoffPointID = "" }, "handoff point id is required"},
		{"handoff kind", func(s *layout.Spec) { s.Handoffs[0].Kind = "SHELF" }, "kind must be DECANT, PACK, DOCK, FORWARD or OTHER"},
		{"duplicate reference", func(s *layout.Spec) { s.Handoffs[1].LocationCode = "WH1-RCV-DEC1" }, `duplicate reference "WH1-RCV-DEC1"`},
		{"duplicate handoff point", func(s *layout.Spec) { s.Handoffs[1].HandoffPointID = "ST1" }, `duplicate handoff point "ST1"`},
		{"duplicate asrs", func(s *layout.Spec) {
			dup := s.Asrs[0]
			dup.LocationCode = "OTHER"
			dup.Ports = nil
			s.Asrs = append(s.Asrs, dup)
		}, "duplicate AS/RS ASRS1"},
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

func TestStationsAreListedInIDOrder(t *testing.T) {
	s := floor()
	s.Stations = append(s.Stations, layout.Station{StationID: "ST0", HandoffPointID: "ST0", BufferLimit: 1})
	l := mustNew(t, s)
	got := l.Stations()
	if len(got) != 2 || got[0].StationID != "ST0" || got[1].StationID != "ST1" {
		t.Fatalf("Stations = %+v", got)
	}
	if p, ok := l.Resolve("ST0"); !ok || p.Code != "ST0" {
		t.Fatalf("a station without a location code resolves by its id: %+v %v", p, ok)
	}
}
