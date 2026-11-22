package containertrack_test

import (
	"errors"
	"testing"
	"time"

	ct "github.com/IQVO/material-flow-control/internal/domain/containertrack"
	"github.com/IQVO/material-flow-control/internal/domain/layout"
)

var t0 = time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)

func TestPositionsOfPlaces(t *testing.T) {
	cases := []struct {
		p    layout.Place
		want ct.Position
	}{
		{layout.Place{Kind: layout.PlaceAsrs, Code: "A-S"}, ct.Position{Kind: ct.InAsrs, Code: "A-S"}},
		{layout.Place{Kind: layout.PlacePort, Code: "A-OUT1"}, ct.Position{Kind: ct.AtPort, Code: "A-OUT1"}},
		{layout.Place{Kind: layout.PlaceStation, Code: "ST1"}, ct.Position{Kind: ct.AtStation, Code: "ST1"}},
		{layout.Place{Kind: layout.PlaceHandoff, Code: "PK"}, ct.Position{Kind: ct.AtHandoff, Code: "PK"}},
		{layout.Place{Kind: layout.PlaceClarification, Code: "CLR"}, ct.Position{Kind: ct.AtClarification, Code: "CLR"}},
	}
	for _, c := range cases {
		if got := ct.At(c.p); got != c.want {
			t.Fatalf("At(%+v) = %+v", c.p, got)
		}
	}
	if got := ct.OnRobotOf("AMR1", "EMU/R1"); got != (ct.Position{Kind: ct.OnRobot, Code: "AMR1", Robot: "EMU/R1"}) {
		t.Fatalf("OnRobotOf = %+v", got)
	}
	inside := map[ct.PositionKind]bool{ct.InAsrs: true, ct.AtPort: true, ct.OnRobot: true, ct.AtStation: false, ct.AtHandoff: false, ct.AtClarification: false, ct.Unknown: false}
	for k, want := range inside {
		if (ct.Position{Kind: k}).Inside() != want {
			t.Fatalf("%s Inside != %v", k, want)
		}
	}
}

func TestAContainerHasAtMostOneActiveOrder(t *testing.T) {
	tr := ct.New("TOTE-1", t0)
	if tr.Position().Kind != ct.Unknown || tr.ContainerID() != "TOTE-1" {
		t.Fatalf("new = %+v", tr.Snapshot())
	}
	if err := tr.Reserve("to-1", "rq-1", t0); err != nil {
		t.Fatal(err)
	}
	if err := tr.Reserve("to-2", "rq-2", t0); !errors.Is(err, ct.ErrBusy) {
		t.Fatalf("second Reserve = %v, want ErrBusy", err)
	}
	if err := tr.Dispatchable(); !errors.Is(err, ct.ErrBusy) {
		t.Fatalf("Dispatchable = %v", err)
	}
	tr.Release("to-9", t0)
	if tr.ActiveOrderID() != "to-1" {
		t.Fatal("another order released the reservation")
	}
	tr.Release("to-1", t0.Add(time.Second))
	if tr.ActiveOrderID() != "" || tr.ActiveRequestID() != "" || tr.Dispatchable() != nil {
		t.Fatalf("after release = %+v", tr.Snapshot())
	}
}

func TestMovesAreRecordedOnceWithTheActiveRequest(t *testing.T) {
	tr := ct.New("TOTE-1", t0)
	_ = tr.Reserve("to-1", "rq-1", t0)
	port := ct.Position{Kind: ct.AtPort, Code: "A-OUT1"}
	tr.MoveTo(port, t0.Add(time.Second))
	tr.MoveTo(port, t0.Add(2*time.Second))
	robot := ct.OnRobotOf("AMR1", "EMU/R1")
	tr.MoveTo(robot, t0.Add(3*time.Second))
	moves := tr.PullMoves()
	if len(moves) != 2 {
		t.Fatalf("moves = %+v (a repeated position is not a move)", moves)
	}
	if moves[0] != (ct.Move{From: ct.Position{Kind: ct.Unknown}, To: port, RequestID: "rq-1", At: t0.Add(time.Second)}) ||
		moves[1] != (ct.Move{From: port, To: robot, RequestID: "rq-1", At: t0.Add(3 * time.Second)}) {
		t.Fatalf("moves = %+v", moves)
	}
	if len(tr.PullMoves()) != 0 {
		t.Fatal("PullMoves drains")
	}
	if s := tr.Snapshot(); s.Position != robot || !s.UpdatedAt.Equal(t0.Add(3*time.Second)) {
		t.Fatalf("snapshot = %+v", s)
	}
}

func TestAContainerAtClarificationIsNeverDispatchedUntilResolved(t *testing.T) {
	tr := ct.New("TOTE-1", t0)
	_ = tr.Reserve("to-1", "rq-1", t0)
	ev := tr.SendToClarification(ct.Clarification{Reason: ct.ReasonNoRead, OrderID: "to-1", RequestID: "rq-1", DetectedAt: "A-OUT1"}, t0)
	want := ct.ContainerSentToClarification{ContainerID: "TOTE-1", OrderID: "to-1", RequestID: "rq-1", Reason: ct.ReasonNoRead, DetectedAt: "A-OUT1", At: t0}
	if ev != want {
		t.Fatalf("event = %+v", ev)
	}
	tr.Release("to-1", t0)
	if err := tr.Dispatchable(); !errors.Is(err, ct.ErrInClarification) {
		t.Fatalf("Dispatchable = %v, want ErrInClarification", err)
	}
	if err := tr.Reserve("to-2", "rq-2", t0); !errors.Is(err, ct.ErrInClarification) {
		t.Fatalf("Reserve = %v", err)
	}
	if err := tr.ReserveInternal("to-int", t0); err != nil {
		t.Fatalf("the clarification move may reserve it: %v", err)
	}
	if err := tr.ReserveInternal("to-int2", t0); !errors.Is(err, ct.ErrBusy) {
		t.Fatalf("a second internal reservation = %v", err)
	}
	tr.Release("to-int", t0)
	c, ok := tr.Clarification()
	if !ok || c.Reason != ct.ReasonNoRead || !c.Since.Equal(t0) {
		t.Fatalf("clarification = %+v %v", c, ok)
	}
	clr := ct.Position{Kind: ct.AtClarification, Code: "CLR"}
	if err := tr.Resolve(&clr, t0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, ok := tr.Clarification(); ok || tr.Dispatchable() != nil || tr.Position() != clr {
		t.Fatalf("resolved = %+v", tr.Snapshot())
	}
	if err := tr.Resolve(nil, t0); !errors.Is(err, ct.ErrNotInClarification) {
		t.Fatalf("second Resolve = %v", err)
	}
}

func TestRestoreRoundTripsTheSnapshot(t *testing.T) {
	c := &ct.Clarification{Reason: ct.ReasonMismatch, ScannedContainerID: "TOTE-9", DetectedAt: "A-OUT1", Since: t0}
	s := ct.Snapshot{ContainerID: "TOTE-1", Position: ct.Position{Kind: ct.AtPort, Code: "A-OUT1"}, ActiveOrderID: "to-1",
		ActiveRequestID: "rq-1", Clarification: c, UpdatedAt: t0, Version: 4}
	got := ct.Restore(s).Snapshot()
	if got.ContainerID != s.ContainerID || got.Position != s.Position || got.ActiveOrderID != "to-1" || got.Version != 4 ||
		got.Clarification == nil || *got.Clarification != *c {
		t.Fatalf("round trip = %+v", got)
	}
	c.Reason = ct.ReasonNoRead
	if got.Clarification.Reason != ct.ReasonMismatch {
		t.Fatal("the snapshot shares the clarification with its caller")
	}
}
