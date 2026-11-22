package routing_test

import (
	"errors"
	"testing"

	"github.com/IQVO/material-flow-control/internal/domain/layout"
	"github.com/IQVO/material-flow-control/internal/domain/routing"
)

func example(t *testing.T) *layout.Layout {
	t.Helper()
	l, err := layout.New(layout.Spec{
		SiteID: "WH1",
		Fleet:  layout.Fleet{FleetID: "AMR1", LegsPerRobotHour: 30},
		Asrs: []layout.Asrs{{AsrsID: "ASRS1", LocationCode: "WH1-AUTO-ASRS-ASRS1-00-00-S", RetrievalsPerUnitHour: 60,
			Ports: []layout.Port{
				{PortID: "IN1", Kind: layout.PortInput, LocationCode: "WH1-AUTO-ASRS-ASRS1-01-00-IN1", HandoffPointID: "ASRS1-IN1"},
				{PortID: "OUT1", Kind: layout.PortOutput, LocationCode: "WH1-AUTO-ASRS-ASRS1-01-00-OUT1", HandoffPointID: "ASRS1-OUT1"},
				{PortID: "IN2", Kind: layout.PortInput, LocationCode: "WH1-AUTO-ASRS-ASRS1-02-00-IN2", HandoffPointID: "ASRS1-IN2"},
			}}},
		Stations:            []layout.Station{{StationID: "ST1", HandoffPointID: "ST1", BufferLimit: 2}},
		Handoffs:            []layout.Handoff{{LocationCode: "WH1-RCV-DEC1", HandoffPointID: "ST2", Kind: layout.HandoffDecant}, {LocationCode: "WH1-PCK-PK1", HandoffPointID: "PK1", Kind: layout.HandoffPack}},
		ClarificationPoints: []layout.ClarificationPoint{{LocationCode: "WH1-CLR-CLR1", HandoffPointID: "CLR1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func place(t *testing.T, l *layout.Layout, ref string) layout.Place {
	t.Helper()
	p, ok := l.Resolve(ref)
	if !ok {
		t.Fatalf("%s does not resolve", ref)
	}
	return p
}

const asrsCode = "WH1-AUTO-ASRS-ASRS1-00-00-S"

func TestFromTheAsrsToAStationIsARetrieveThenAMove(t *testing.T) {
	l := example(t)
	legs, err := routing.Plan(l, place(t, l, asrsCode), place(t, l, "ST1"))
	if err != nil {
		t.Fatal(err)
	}
	if len(legs) != 2 {
		t.Fatalf("legs = %+v", legs)
	}
	r, m := legs[0], legs[1]
	if r.Subsystem != routing.SubsystemAsrs || r.SubsystemID != "ASRS1" || r.Kind != routing.KindRetrieve || r.From.Code != asrsCode || !r.To.IsZero() {
		t.Fatalf("retrieve = %+v", r)
	}
	if m.Subsystem != routing.SubsystemAmr || m.SubsystemID != "AMR1" || m.Kind != routing.KindMove || !m.From.IsZero() || m.To.Code != "ST1" {
		t.Fatalf("move = %+v (its From is the presented port, resolved later)", m)
	}
}

func TestIntoTheAsrsIsAMoveThenAStore(t *testing.T) {
	l := example(t)
	legs, err := routing.Plan(l, place(t, l, "WH1-RCV-DEC1"), place(t, l, asrsCode))
	if err != nil {
		t.Fatal(err)
	}
	if len(legs) != 2 || legs[0].Kind != routing.KindMove || legs[0].From.HandoffPointID != "ST2" || !legs[0].To.IsZero() ||
		legs[1].Kind != routing.KindStore || legs[1].SubsystemID != "ASRS1" || !legs[1].From.IsZero() || legs[1].To.Code != asrsCode {
		t.Fatalf("legs = %+v", legs)
	}
}

func TestBetweenTwoAMRPlacesIsOneMove(t *testing.T) {
	l := example(t)
	legs, err := routing.Plan(l, place(t, l, "ST1"), place(t, l, "WH1-PCK-PK1"))
	if err != nil {
		t.Fatal(err)
	}
	if len(legs) != 1 || legs[0].Kind != routing.KindMove || legs[0].From.Code != "ST1" || legs[0].To.Code != "WH1-PCK-PK1" || legs[0].SubsystemID != "AMR1" {
		t.Fatalf("legs = %+v", legs)
	}
	fromPort, err := routing.Plan(l, place(t, l, "WH1-AUTO-ASRS-ASRS1-01-00-OUT1"), place(t, l, "WH1-CLR-CLR1"))
	if err != nil || len(fromPort) != 1 || fromPort[0].From.PortID != "OUT1" || fromPort[0].To.HandoffPointID != "CLR1" {
		t.Fatalf("from a port = %+v %v", fromPort, err)
	}
}

func TestUnreachableAndSamePlaceRoutesAreRefused(t *testing.T) {
	l := example(t)
	cases := []struct {
		src, dst string
		want     error
	}{
		{asrsCode, asrsCode, routing.ErrSamePlace},
		{"ST1", "ST1", routing.ErrSamePlace},
		{"ST1", "WH1-AUTO-ASRS-ASRS1-01-00-IN1", routing.ErrUnreachable},
		{asrsCode, "WH1-AUTO-ASRS-ASRS1-01-00-OUT1", routing.ErrUnreachable},
	}
	for _, c := range cases {
		if _, err := routing.Plan(l, place(t, l, c.src), place(t, l, c.dst)); !errors.Is(err, c.want) {
			t.Fatalf("Plan(%s, %s) = %v, want %v", c.src, c.dst, err, c.want)
		}
	}
	other := layout.Place{Kind: layout.PlaceAsrs, Code: "WH1-AUTO-ASRS-ASRS2-00-00-S", AsrsID: "ASRS2"}
	if _, err := routing.Plan(l, place(t, l, asrsCode), other); !errors.Is(err, routing.ErrUnreachable) {
		t.Fatalf("AS/RS to AS/RS = %v", err)
	}
	noHandoff := layout.Place{Kind: layout.PlaceHandoff, Code: "X"}
	if _, err := routing.Plan(l, noHandoff, place(t, l, "ST1")); !errors.Is(err, routing.ErrUnreachable) {
		t.Fatalf("a place no robot reaches = %v", err)
	}
	if _, err := routing.Plan(l, noHandoff, place(t, l, asrsCode)); !errors.Is(err, routing.ErrUnreachable) {
		t.Fatalf("a place no robot reaches, into the AS/RS = %v", err)
	}
	if _, err := routing.Plan(l, place(t, l, asrsCode), noHandoff); !errors.Is(err, routing.ErrUnreachable) {
		t.Fatalf("out of the AS/RS to a place no robot reaches = %v", err)
	}
}

func TestAFailedStoreIsReplannedThroughTheNextInputPort(t *testing.T) {
	l := example(t)
	asrs := place(t, l, asrsCode)
	legs, err := routing.StoreViaOtherInput(l, place(t, l, "WH1-AUTO-ASRS-ASRS1-01-00-IN1"), asrs)
	if err != nil {
		t.Fatal(err)
	}
	if len(legs) != 2 || legs[0].Kind != routing.KindMove || legs[0].From.PortID != "IN1" || legs[0].To.PortID != "IN2" ||
		legs[1].Kind != routing.KindStore || legs[1].From.PortID != "IN2" || legs[1].To.Code != asrsCode {
		t.Fatalf("re-plan = %+v", legs)
	}
	if _, err := routing.StoreViaOtherInput(l, place(t, l, "WH1-AUTO-ASRS-ASRS1-02-00-IN2"), asrs); err != nil {
		t.Fatalf("IN2 re-plans through IN1: %v", err)
	}
	single, _ := layout.New(layout.Spec{SiteID: "S", Fleet: layout.Fleet{FleetID: "F", LegsPerRobotHour: 1},
		Asrs: []layout.Asrs{{AsrsID: "A", LocationCode: "A-S", RetrievalsPerUnitHour: 1, Ports: []layout.Port{
			{PortID: "IN1", Kind: layout.PortInput, LocationCode: "A-IN1", HandoffPointID: "H1"},
			{PortID: "OUT1", Kind: layout.PortOutput, LocationCode: "A-OUT1", HandoffPointID: "H2"}}}},
		ClarificationPoints: []layout.ClarificationPoint{{LocationCode: "C", HandoffPointID: "HC"}}})
	in1, _ := single.Resolve("A-IN1")
	a, _ := single.Resolve("A-S")
	if _, err := routing.StoreViaOtherInput(single, in1, a); !errors.Is(err, routing.ErrUnreachable) {
		t.Fatalf("no other input port = %v", err)
	}
}

// Added beyond the brief (Task 3 D1): the brief's tests never route an AMR place to a destination no robot
// reaches, which left Plan's last refusal uncovered (and its mutants unkilled for Task 26).
func TestFromAnAMRPlaceToAPlaceNoRobotReachesIsUnreachable(t *testing.T) {
	l := example(t)
	noHandoff := layout.Place{Kind: layout.PlaceHandoff, Code: "X"}
	if legs, err := routing.Plan(l, place(t, l, "ST1"), noHandoff); !errors.Is(err, routing.ErrUnreachable) || legs != nil {
		t.Fatalf("Plan(ST1, X) = %+v %v, want ErrUnreachable", legs, err)
	}
}
