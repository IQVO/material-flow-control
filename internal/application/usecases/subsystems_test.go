package usecases_test

import (
	"io"
	"log/slog"
	"testing"

	"github.com/IQVO/material-flow-control/internal/adapters/outbound/memory"
	"github.com/IQVO/material-flow-control/internal/application/usecases"
	"github.com/IQVO/material-flow-control/internal/domain/capacity"
	"github.com/IQVO/material-flow-control/internal/domain/layout"
)

// The /subsystems contract (openapi: "Every subsystem and station snapshot, sorted by kind then id (a
// bounded set, one per layout entry)"): a station the layout names appears from the layout alone —
// empty and AVAILABLE — before its first arrival. Deploy verification 2026-11-22: STATION/ST1 was
// absent from GET /subsystems because a snapshot row is written only when a flow first touches the
// station's queue, so a station nobody ever delivered to was invisible to relationship 2.
func TestSubsystemsListsEveryLayoutStationBeforeItsFirstArrival(t *testing.T) {
	e := newEnv(t)
	subs, err := e.svc.Subsystems(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []capacity.Snapshot{capacity.Station("ST1", 2, 0, 0, t0)}
	if len(subs) != len(want) || subs[0] != want[0] {
		t.Fatalf("subsystems = %+v, want only the derived layout station %+v", subs, want)
	}
}

// A station a flow touched keeps its persisted snapshot (measured, occupied); an untouched sibling is
// derived from the layout, and everything sorts by kind then id like the persisted List does.
func TestSubsystemsMergesPersistedStationsWithLayoutEntriesSortedByKindThenID(t *testing.T) {
	e := newEnvWithLayout(t, twoStationFloor(t))
	fleet := capacity.Fleet("AMR1", 3, 3, 2, 1, 1, 30, t0)
	asrs := capacity.Asrs("ASRS1", 2, 2, 0, 60, t0)
	touched := capacity.Station("ST1", 2, 2, 1, t0) // full buffer, one arrival waiting upstream
	for _, s := range []capacity.Snapshot{fleet, asrs, touched} {
		if err := e.caps.Save(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	subs, err := e.svc.Subsystems(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []capacity.Snapshot{fleet, asrs, touched, capacity.Station("ST9", 1, 0, 0, t0)}
	if len(subs) != len(want) {
		t.Fatalf("subsystems = %+v, want %+v", subs, want)
	}
	for i := range want {
		if subs[i] != want[i] {
			t.Fatalf("subsystems[%d] = %+v, want %+v (whole list %+v)", i, subs[i], want[i], subs)
		}
	}
}

// twoStationFloor is the example floor with a second, never-touched station ST9.
func twoStationFloor(t *testing.T) *layout.Layout {
	spec := layout.Spec{
		SiteID: "WH1",
		Fleet:  layout.Fleet{FleetID: "AMR1", LegsPerRobotHour: 30},
		Asrs: []layout.Asrs{{
			AsrsID: "ASRS1", LocationCode: "WH1-AUTO-ASRS-ASRS1-00-00-S", RetrievalsPerUnitHour: 60,
			Ports: []layout.Port{
				{PortID: "IN1", Kind: layout.PortInput, LocationCode: "WH1-AUTO-ASRS-ASRS1-01-00-IN1", HandoffPointID: "ASRS1-IN1"},
				{PortID: "OUT1", Kind: layout.PortOutput, LocationCode: "WH1-AUTO-ASRS-ASRS1-01-00-OUT1", HandoffPointID: "ASRS1-OUT1"},
			},
		}},
		Stations: []layout.Station{
			{StationID: "ST1", LocationCode: "WH1-GTP-ST1", HandoffPointID: "ST1", BufferLimit: 2},
			{StationID: "ST9", LocationCode: "WH1-GTP-ST9", HandoffPointID: "ST9", BufferLimit: 1},
		},
		Handoffs: []layout.Handoff{
			{LocationCode: "WH1-RCV-DEC1", HandoffPointID: "DEC1", Kind: layout.HandoffDecant},
		},
		ClarificationPoints: []layout.ClarificationPoint{{LocationCode: "WH1-CLR-CLR1", HandoffPointID: "CLR1"}},
	}
	l, err := layout.New(spec)
	if err != nil {
		t.Fatalf("twoStationFloor: %v", err)
	}
	return l
}

// newEnvWithLayout is newEnv with the layout replaced (the derived-station view reads the layout).
func newEnvWithLayout(t *testing.T, l *layout.Layout) *env {
	t.Helper()
	s := memory.NewStore()
	clock := memory.NewClock(t0)
	caps := memory.NewCapacities(s)
	e := &env{t: t, svc: nil, store: s, clock: clock, events: memory.NewEvents(s), cmds: memory.NewCommands(s),
		tracks: memory.NewTracks(s), caps: caps, orders: memory.NewOrders(s), asrs: memory.NewAsrs(),
		amr: memory.NewAmr(), metrics: &memory.Metrics{}}
	var err error
	e.svc, err = usecases.NewService(l, usecases.DefaultConfig(), usecases.Deps{
		UoW: memory.NewUnitOfWork(s), Lock: memory.FlowLock{}, Requests: memory.NewRequests(s), Orders: e.orders, Tracks: e.tracks,
		Stations: memory.NewStations(s), Capacities: caps, Units: memory.NewUnits(s), Commands: e.cmds, Inbox: memory.NewInbox(s),
		Sink: e.events, Asrs: e.asrs, Amr: e.amr, Clock: clock, IDs: memory.NewIDs("u"), Metrics: e.metrics, Kick: &memory.Kicker{},
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	return e
}
