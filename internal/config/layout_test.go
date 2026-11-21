package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/IQVO/material-flow-control/internal/config"
	"github.com/IQVO/material-flow-control/internal/domain/layout"
)

// TestTheExampleLayoutLoads is the plan 04 Task 2 brief's test split for cyclop (max 15): the reference loop moved
// verbatim into assertExampleRefsResolve; every assertion is byte-identical to the brief.
func TestTheExampleLayoutLoads(t *testing.T) {
	l, err := config.LoadLayout("../../config/flow-layout.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if l.SiteID() != "WH1" || l.Fleet().FleetID != "AMR1" || l.Fleet().LegsPerRobotHour != 30 {
		t.Fatalf("header = %s %+v", l.SiteID(), l.Fleet())
	}
	a, ok := l.Asrs("ASRS1")
	if !ok || a.RetrievalsPerUnitHour != 60 || len(a.Ports) != 4 {
		t.Fatalf("ASRS1 = %+v %v", a, ok)
	}
	if p, ok := l.Resolve("WH1-AUTO-ASRS-ASRS1-01-00-OUT1"); !ok || p.HandoffPointID != "ASRS1-OUT1" || p.PortKind != layout.PortOutput {
		t.Fatalf("OUT1 = %+v %v", p, ok)
	}
	if st, ok := l.Station("ST1"); !ok || st.BufferLimit != 2 || st.HandoffPointID != "ST1" {
		t.Fatalf("ST1 = %+v %v", st, ok)
	}
	assertExampleRefsResolve(t, l)
}

func assertExampleRefsResolve(t *testing.T, l *layout.Layout) {
	t.Helper()
	for _, ref := range []string{"WH1-RCV-DEC1", "WH1-PCK-PK1", "WH1-FWD-FWD1", "WH1-CLR-CLR1", "WH1-GTP-ST1"} {
		if _, ok := l.Resolve(ref); !ok {
			t.Fatalf("%s does not resolve", ref)
		}
	}
}

func TestRatesDefaultWhenOmitted(t *testing.T) {
	l, err := config.ParseLayout([]byte(`siteId: S
fleet: {fleetId: F}
asrs:
  - asrsId: A
    locationCode: S-AUTO-ASRS-A-00-00-S
    ports:
      - {portId: IN1, kind: INPUT, locationCode: S-IN1, handoffPointId: H-IN1}
      - {portId: OUT1, kind: OUTPUT, locationCode: S-OUT1, handoffPointId: H-OUT1}
clarificationPoints:
  - {locationCode: S-CLR, handoffPointId: H-CLR}
`))
	if err != nil {
		t.Fatal(err)
	}
	if a, _ := l.Asrs("A"); a.RetrievalsPerUnitHour != 60 || l.Fleet().LegsPerRobotHour != 30 {
		t.Fatalf("defaults = %d %d", a.RetrievalsPerUnitHour, l.Fleet().LegsPerRobotHour)
	}
}

func TestABrokenFileNamesItsProblem(t *testing.T) {
	if _, err := config.ParseLayout([]byte("siteId: [")); err == nil {
		t.Fatal("malformed YAML parsed")
	}
	if _, err := config.ParseLayout([]byte("siteId: S\nunknownKey: 1\n")); err == nil {
		t.Fatal("an unknown key was accepted")
	}
	if _, err := config.ParseLayout([]byte("siteId: S\nfleet: {fleetId: F}\n")); !errors.Is(err, layout.ErrInvalid) {
		t.Fatalf("an invalid layout = %v, want ErrInvalid", err)
	}
	if _, err := config.LoadLayout(filepath.Join(t.TempDir(), "missing.yaml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing file = %v", err)
	}
}
