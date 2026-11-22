package transportorder_test

import (
	"errors"
	"testing"
	"time"

	"github.com/IQVO/material-flow-control/internal/domain/layout"
	"github.com/IQVO/material-flow-control/internal/domain/routing"
	to "github.com/IQVO/material-flow-control/internal/domain/transportorder"
	tr "github.com/IQVO/material-flow-control/internal/domain/transportrequest"
)

var t0 = time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)

var (
	asrs    = layout.Place{Kind: layout.PlaceAsrs, Code: "WH1-AUTO-ASRS-ASRS1-00-00-S", AsrsID: "ASRS1"}
	out1    = layout.Place{Kind: layout.PlacePort, Code: "WH1-AUTO-ASRS-ASRS1-01-00-OUT1", AsrsID: "ASRS1", PortID: "OUT1", PortKind: layout.PortOutput, HandoffPointID: "ASRS1-OUT1"}
	in1     = layout.Place{Kind: layout.PlacePort, Code: "WH1-AUTO-ASRS-ASRS1-01-00-IN1", AsrsID: "ASRS1", PortID: "IN1", PortKind: layout.PortInput, HandoffPointID: "ASRS1-IN1"}
	in2     = layout.Place{Kind: layout.PlacePort, Code: "WH1-AUTO-ASRS-ASRS1-02-00-IN2", AsrsID: "ASRS1", PortID: "IN2", PortKind: layout.PortInput, HandoffPointID: "ASRS1-IN2"}
	station = layout.Place{Kind: layout.PlaceStation, Code: "ST1", StationID: "ST1", HandoffPointID: "ST1"}
	decant  = layout.Place{Kind: layout.PlaceHandoff, Code: "WH1-RCV-DEC1", HandoffPointID: "ST2", HandoffKind: layout.HandoffDecant}
	pack    = layout.Place{Kind: layout.PlaceHandoff, Code: "WH1-PCK-PK1", HandoffPointID: "PK1", HandoffKind: layout.HandoffPack}
)

func req(purpose tr.Purpose, src, dst layout.Place) tr.Request {
	return tr.Request{ID: "rq-1", ContainerID: "TOTE-1", Source: src.Code, Destination: dst.Code, Purpose: purpose,
		Priority: tr.PriorityNormal, DueBy: t0.Add(time.Hour)}
}

func pickPlan() []routing.LegPlan {
	return []routing.LegPlan{
		{Subsystem: routing.SubsystemAsrs, SubsystemID: "ASRS1", Kind: routing.KindRetrieve, From: asrs},
		{Subsystem: routing.SubsystemAmr, SubsystemID: "AMR1", Kind: routing.KindMove, To: station},
	}
}

func putawayPlan() []routing.LegPlan {
	return []routing.LegPlan{
		{Subsystem: routing.SubsystemAmr, SubsystemID: "AMR1", Kind: routing.KindMove, From: decant},
		{Subsystem: routing.SubsystemAsrs, SubsystemID: "ASRS1", Kind: routing.KindStore, To: asrs},
	}
}

func accept(t *testing.T, purpose tr.Purpose, src, dst layout.Place, plan []routing.LegPlan) *to.Order {
	t.Helper()
	o, err := to.Accept("to-1", req(purpose, src, dst), src, dst, plan, t0)
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// TestAGoodsToPersonPickRunsRetrieveThenWaitsForTheStationThenMoves is the plan 04 Task 6 brief's test split for
// cyclop (max 15): its three phases moved verbatim, in order, into the helpers below; every assertion is
// byte-identical to the brief (only `evs = o.PullEvents()` became `evs := o.PullEvents()` in the last helper).
func TestAGoodsToPersonPickRunsRetrieveThenWaitsForTheStationThenMoves(t *testing.T) {
	o := accept(t, tr.PurposePick, asrs, station, pickPlan())
	assertTheRetrieveIsRequestedThenPresented(t, o)
	assertTheMoveWaitsForTheStationThenIsDelivered(t, o)
	assertTheOrderCompletesOnceAtTheStation(t, o)
}

func assertTheRetrieveIsRequestedThenPresented(t *testing.T, o *to.Order) {
	t.Helper()
	evs := o.PullEvents()
	acc, ok := evs[0].(to.TransportAccepted)
	if len(evs) != 1 || !ok || acc != (to.TransportAccepted{OrderID: "to-1", RequestID: "rq-1", ContainerID: "TOTE-1", Source: asrs.Code,
		Destination: "ST1", Purpose: tr.PurposePick, Priority: tr.PriorityNormal, DueBy: t0.Add(time.Hour), Legs: 2, At: t0}) {
		t.Fatalf("events = %+v", evs)
	}
	leg, act := o.Next(t0)
	if act != to.ActionRequest || leg.Ref != "to-1.1.1" || leg.Kind != routing.KindRetrieve || leg.Status != to.LegPending {
		t.Fatalf("first = %+v %s", leg, act)
	}
	if _, again := o.Next(t0); again != to.ActionNone {
		t.Fatalf("a second Next while the retrieve is in flight = %s", again)
	}
	must(t, o.Requested("to-1.1.1", "sj-7", t0.Add(time.Second)))
	must(t, o.Presented("to-1.1.1", out1, t0.Add(time.Minute)))
}

func assertTheMoveWaitsForTheStationThenIsDelivered(t *testing.T, o *to.Order) {
	t.Helper()
	move, act := o.Next(t0.Add(time.Minute))
	if act != to.ActionAwaitStation || move.Ref != "to-1.2.1" || move.Status != to.LegWaitingStation || move.From != out1 || move.To != station {
		t.Fatalf("move = %+v %s (From is the presented port)", move, act)
	}
	admitted, err := o.Admitted(t0.Add(time.Minute))
	if err != nil || admitted.Status != to.LegPending || admitted.Ref != "to-1.2.1" {
		t.Fatalf("Admitted = %+v %v", admitted, err)
	}
	must(t, o.Assigned("to-1.2.1", "EMU/R1", t0.Add(2*time.Minute)))
	must(t, o.PickedUp("to-1.2.1", "EMU/R1", t0.Add(3*time.Minute)))
	if prev, ok := o.Previous("to-1.2.1"); !ok || prev.Kind != routing.KindRetrieve || prev.To != out1 {
		t.Fatalf("Previous = %+v %v", prev, ok)
	}
	must(t, o.Delivered("to-1.2.1", t0.Add(4*time.Minute)))
}

func assertTheOrderCompletesOnceAtTheStation(t *testing.T, o *to.Order) {
	t.Helper()
	if _, act := o.Next(t0.Add(4 * time.Minute)); act != to.ActionComplete {
		t.Fatalf("after the last leg = %s", act)
	}
	must(t, o.Complete(1, t0.Add(4*time.Minute)))
	evs := o.PullEvents()
	arr, ok1 := evs[0].(to.ContainerArrivedAtStation)
	done, ok2 := evs[1].(to.TransportCompleted)
	if len(evs) != 2 || !ok1 || !ok2 || arr.StationID != "ST1" || arr.QueuePosition != 1 || arr.Purpose != tr.PurposePick ||
		done != (to.TransportCompleted{OrderID: "to-1", RequestID: "rq-1", ContainerID: "TOTE-1", Destination: "ST1", Purpose: tr.PurposePick, At: t0.Add(4 * time.Minute)}) {
		t.Fatalf("events = %+v", evs)
	}
	if o.Status() != to.StatusCompleted || !o.Ended() {
		t.Fatalf("status = %s", o.Status())
	}
	if err := o.Complete(1, t0); !errors.Is(err, to.ErrEnded) {
		t.Fatalf("second Complete = %v", err)
	}
}

func TestAPutawayResolvesTheInputPortAndStoresThere(t *testing.T) {
	o := accept(t, tr.PurposePutaway, decant, asrs, putawayPlan())
	o.PullEvents()
	move, act := o.Next(t0)
	if act != to.ActionRequest || move.From != decant || !move.To.IsZero() {
		t.Fatalf("move = %+v %s", move, act)
	}
	must(t, o.ResolveTo(move.Ref, in1))
	if err := o.ResolveTo(move.Ref, in2); !errors.Is(err, to.ErrNotInFlight) {
		t.Fatalf("a second resolution = %v", err)
	}
	must(t, o.Delivered(move.Ref, t0.Add(time.Minute)))
	store, act := o.Next(t0.Add(time.Minute))
	if act != to.ActionRequest || store.Kind != routing.KindStore || store.From != in1 || store.To != asrs || store.Ref != "to-1.2.1" {
		t.Fatalf("store = %+v %s", store, act)
	}
	must(t, o.Stored(store.Ref, t0.Add(2*time.Minute)))
	if _, act := o.Next(t0); act != to.ActionComplete {
		t.Fatalf("act = %s", act)
	}
	must(t, o.Complete(0, t0.Add(2*time.Minute)))
	evs := o.PullEvents()
	if len(evs) != 1 {
		t.Fatalf("a transport to the AS/RS publishes only TransportCompleted: %+v", evs)
	}
}

func TestALegNeverStartsBeforeThePreviousHandoff(t *testing.T) {
	o := accept(t, tr.PurposePutaway, decant, asrs, putawayPlan())
	o.Next(t0)
	if err := o.Stored("to-1.2.1", t0); !errors.Is(err, to.ErrNotInFlight) {
		t.Fatalf("a store fact before the move delivered = %v, want ErrNotInFlight", err)
	}
	if err := o.Delivered("to-1.9.1", t0); !errors.Is(err, to.ErrUnknownLeg) {
		t.Fatalf("an unknown ref = %v", err)
	}
	if l, _ := o.LegByRef("to-1.2.1"); l.Status != to.LegPlanned {
		t.Fatalf("the store = %s", l.Status)
	}
}

func TestATransportEndsInExactlyOneOutcome(t *testing.T) {
	o := accept(t, tr.PurposeOutbound, station, pack, []routing.LegPlan{{Subsystem: routing.SubsystemAmr, SubsystemID: "AMR1", Kind: routing.KindMove, From: station, To: pack}})
	o.PullEvents()
	o.Next(t0)
	must(t, o.Fail(to.FailSubsystemFault, "ROBOT_FAULT", "ST1", t0.Add(time.Minute)))
	evs := o.PullEvents()
	want := to.TransportFailed{OrderID: "to-1", RequestID: "rq-1", ContainerID: "TOTE-1", Purpose: tr.PurposeOutbound, Reason: to.FailSubsystemFault,
		SubsystemReason: "ROBOT_FAULT", ContainerLocation: "ST1", At: t0.Add(time.Minute)}
	if len(evs) != 1 || evs[0] != want {
		t.Fatalf("events = %+v", evs)
	}
	if l := o.Legs()[0]; l.Status != to.LegCancelled || !l.EndedAt.Equal(t0.Add(time.Minute)) {
		t.Fatalf("the abandoned leg = %+v", l)
	}
	if err := o.Fail(to.FailTimeout, "", "", t0); !errors.Is(err, to.ErrEnded) {
		t.Fatalf("second Fail = %v", err)
	}
	if err := o.Complete(0, t0); !errors.Is(err, to.ErrEnded) {
		t.Fatalf("Complete after Fail = %v", err)
	}
	if o.Reason() != to.FailSubsystemFault || o.Status() != to.StatusFailed {
		t.Fatalf("outcome = %s %s", o.Status(), o.Reason())
	}
}

func TestAnInternalOrderPublishesNothing(t *testing.T) {
	clr := layout.Place{Kind: layout.PlaceClarification, Code: "WH1-CLR-CLR1", HandoffPointID: "CLR1"}
	o, err := to.NewInternal("to-9", "TOTE-1", out1, clr, []routing.LegPlan{{Subsystem: routing.SubsystemAmr, SubsystemID: "AMR1", Kind: routing.KindMove, From: out1, To: clr}}, t0)
	if err != nil {
		t.Fatal(err)
	}
	leg, act := o.Next(t0)
	if act != to.ActionRequest || leg.From != out1 {
		t.Fatalf("leg = %+v %s", leg, act)
	}
	must(t, o.Delivered(leg.Ref, t0))
	must(t, o.Complete(0, t0))
	if evs := o.PullEvents(); len(evs) != 0 || !o.Internal() || o.Purpose() != "" {
		t.Fatalf("internal events = %+v", evs)
	}
	if _, err := to.Accept("to-2", req(tr.PurposePick, asrs, station), asrs, station, nil, t0); !errors.Is(err, to.ErrNoLegs) {
		t.Fatalf("no legs = %v", err)
	}
}

func TestCancellationDependsOnWhereTheContainerIs(t *testing.T) {
	move := []routing.LegPlan{{Subsystem: routing.SubsystemAmr, SubsystemID: "AMR1", Kind: routing.KindMove, From: decant, To: station}}
	waiting := accept(t, tr.PurposePick, decant, station, move)
	waiting.Next(t0)
	if d, _, err := waiting.RequestCancel(t0); err != nil || d != to.CancelNow {
		t.Fatalf("waiting for the station at a handoff = %s %v", d, err)
	}

	inFlight := accept(t, tr.PurposeOutbound, decant, pack, []routing.LegPlan{{Subsystem: routing.SubsystemAmr, SubsystemID: "AMR1", Kind: routing.KindMove, From: decant, To: pack}})
	inFlight.Next(t0)
	d, leg, err := inFlight.RequestCancel(t0)
	if err != nil || d != to.CancelAsk || leg.Ref != "to-1.1.1" || !inFlight.CancelRequested() {
		t.Fatalf("in flight = %s %+v %v", d, leg, err)
	}
	if d, _, _ := inFlight.RequestCancel(t0); d != to.CancelAlreadyAsked {
		t.Fatalf("asked twice = %s", d)
	}
	inFlight.CancelRefused(t0)
	if inFlight.CancelRequested() {
		t.Fatal("a refused cancellation lets the transport run on")
	}
	must(t, inFlight.PickedUp("to-1.1.1", "EMU/R1", t0))
	if _, _, err := inFlight.RequestCancel(t0); !errors.Is(err, to.ErrNotCancellable) {
		t.Fatalf("container on a robot = %v", err)
	}

	atPort := accept(t, tr.PurposePick, asrs, station, pickPlan())
	atPort.Next(t0)
	must(t, atPort.Presented("to-1.1.1", out1, t0))
	atPort.Next(t0)
	if _, _, err := atPort.RequestCancel(t0); !errors.Is(err, to.ErrNotCancellable) {
		t.Fatalf("waiting on an output port = %v", err)
	}
	must(t, atPort.Fail(to.FailTimeout, "", "", t0))
	if _, _, err := atPort.RequestCancel(t0); !errors.Is(err, to.ErrEnded) {
		t.Fatalf("ended = %v", err)
	}
}

func TestRestoreRoundTrips(t *testing.T) {
	o := accept(t, tr.PurposePick, asrs, station, pickPlan())
	o.Next(t0)
	s := o.Snapshot()
	r := to.Restore(s)
	if r.ID() != "to-1" || len(r.Legs()) != 2 || r.Legs()[0].Status != to.LegPending || len(r.PullEvents()) != 0 {
		t.Fatalf("restored = %+v", r.Snapshot())
	}
	s.Legs[0].Status = to.LegDone
	if r.Legs()[0].Status != to.LegPending {
		t.Fatal("the snapshot shares its legs")
	}
}
