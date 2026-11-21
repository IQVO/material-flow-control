package usecases_test

import (
	"errors"
	"testing"
	"time"

	"github.com/IQVO/material-flow-control/internal/application/command"
	"github.com/IQVO/material-flow-control/internal/application/usecases"
	"github.com/IQVO/material-flow-control/internal/domain/capacity"
	ct "github.com/IQVO/material-flow-control/internal/domain/containertrack"
	tr "github.com/IQVO/material-flow-control/internal/domain/transportrequest"
)

func rejected(t *testing.T, err error, want tr.Reason) {
	t.Helper()
	var r *usecases.RejectedError
	if !errors.As(err, &r) || r.Reason != want {
		t.Fatalf("err = %v, want a rejection %s", err, want)
	}
}

func TestAPickFromTheAsrsIsAcceptedAndItsRetrieveQueued(t *testing.T) {
	e := newEnv(t)
	e.stored("TOTE-1")
	v := e.mustAccept("rq-1", "TOTE-1", asrsLoc, "ST1", tr.PurposePick)
	if v.Status != usecases.ViewInProgress || v.OrderID == "" || v.LegsTotal != 2 || v.LegsDone != 0 || v.ContainerLocation != asrsLoc ||
		!v.AcceptedAt.Equal(t0) || v.Purpose != tr.PurposePick {
		t.Fatalf("view = %+v", v)
	}
	reqs := e.pending(command.RequestLeg)
	if len(reqs) != 1 || reqs[0].LegRef != v.OrderID+".1.1" || reqs[0].Key != v.OrderID+".1.1" {
		t.Fatalf("commands = %+v", reqs)
	}
	if got := e.types(); !equal(got, []string{"transportorder.TransportAccepted"}) {
		t.Fatalf("events = %v", got)
	}
	track, _ := e.tracks.Get(ctx, "TOTE-1")
	if track.ActiveOrderID() != v.OrderID || track.ActiveRequestID() != "rq-1" {
		t.Fatalf("track = %+v", track.Snapshot())
	}
}

func TestTheFourRejectionsAreRecordedAndPublished(t *testing.T) {
	e := newEnv(t)
	_, err := e.request("rq-u", "TOTE-1", "WH1-NOWHERE", "ST1", tr.PurposePick)
	rejected(t, err, tr.ReasonDestinationUnreachable)
	_, err = e.request("rq-k", "TOTE-9", asrsLoc, "ST1", tr.PurposePick)
	rejected(t, err, tr.ReasonUnknownContainer)
	e.stored("TOTE-1")
	e.mustAccept("rq-1", "TOTE-1", asrsLoc, "ST1", tr.PurposePick)
	_, err = e.request("rq-2", "TOTE-1", asrsLoc, "ST1", tr.PurposePick)
	rejected(t, err, tr.ReasonContainerBusy)
	e.stored("TOTE-2")
	_ = e.caps.Save(ctx, capacity.Asrs("ASRS1", 2, 0, 0, 60, t0))
	_, err = e.request("rq-3", "TOTE-2", asrsLoc, "ST1", tr.PurposePick)
	rejected(t, err, tr.ReasonSubsystemUnavailable)
	want := []string{"transportrequest.TransportRejected", "transportrequest.TransportRejected", "transportorder.TransportAccepted",
		"transportrequest.TransportRejected", "transportrequest.TransportRejected"}
	if got := e.types(); !equal(got, want) {
		t.Fatalf("events = %v", got)
	}
	open, _ := e.orders.Open(ctx)
	if len(open) != 1 {
		t.Fatalf("a rejection created an order: %d open", len(open))
	}
	v, err := e.svc.Transport(ctx, "rq-2")
	if err != nil || v.Status != usecases.ViewRejected || v.Reason != "CONTAINER_BUSY" || v.OrderID != "" || !v.RejectedAt.Equal(t0) {
		t.Fatalf("rejected view = %+v %v", v, err)
	}
}

func TestAContainerAtClarificationIsBusyAndOneSomewhereElseInsideIsUnknown(t *testing.T) {
	e := newEnv(t)
	held := ct.New("TOTE-1", t0)
	held.MoveTo(ct.Position{Kind: ct.AtClarification, Code: clrLoc}, t0)
	held.SendToClarification(ct.Clarification{Reason: ct.ReasonNoRead}, t0)
	_ = e.tracks.Save(ctx, held)
	_, err := e.request("rq-1", "TOTE-1", clrLoc, asrsLoc, tr.PurposePutaway)
	rejected(t, err, tr.ReasonContainerBusy)
	onPort := ct.New("TOTE-2", t0)
	onPort.MoveTo(ct.Position{Kind: ct.AtPort, Code: out1Loc}, t0)
	_ = e.tracks.Save(ctx, onPort)
	_, err = e.request("rq-2", "TOTE-2", decant, asrsLoc, tr.PurposePutaway)
	rejected(t, err, tr.ReasonUnknownContainer)
	if _, err := e.request("rq-3", "TOTE-2", out1Loc, pack, tr.PurposeOutbound); err != nil {
		t.Fatalf("from the port it stands on: %v", err)
	}
	carried := ct.New("TOTE-3", t0)
	carried.MoveTo(ct.Position{Kind: ct.AtHandoff, Code: pack}, t0)
	_ = e.tracks.Save(ctx, carried)
	e.mustAccept("rq-4", "TOTE-3", decant, asrsLoc, tr.PurposePutaway)
	if tk, _ := e.tracks.Get(ctx, "TOTE-3"); tk.Position() != (ct.Position{Kind: ct.AtHandoff, Code: decant}) {
		t.Fatalf("a container a person carried is where the request says: %+v", tk.Position())
	}
}

func TestARetriedRequestReturnsTheOriginalOutcomeAndCreatesNothing(t *testing.T) {
	e := newEnv(t)
	e.stored("TOTE-1")
	first := e.mustAccept("rq-1", "TOTE-1", asrsLoc, "ST1", tr.PurposePick)
	again := e.mustAccept("rq-1", "TOTE-1", asrsLoc, "ST1", tr.PurposePick)
	if again.OrderID != first.OrderID || len(e.pending(command.RequestLeg)) != 1 || len(e.events.All()) != 1 {
		t.Fatalf("replay = %+v, %d commands, %d events", again, len(e.pending(command.RequestLeg)), len(e.events.All()))
	}
	if _, err := e.request("rq-1", "TOTE-1", asrsLoc, "ST1", tr.PurposeReplenish); !errors.Is(err, usecases.ErrRequestConflict) {
		t.Fatalf("same id, another (valid) purpose = %v, want ErrRequestConflict", err)
	}
	if _, err := e.svc.RequestTransport(ctx, tr.Request{ID: "rq-1", ContainerID: "TOTE-1", Source: asrsLoc, Destination: "ST1",
		Purpose: tr.PurposePick, Priority: tr.PriorityUrgent, DueBy: t0.Add(time.Hour)}); !errors.Is(err, usecases.ErrRequestConflict) {
		t.Fatalf("same id, other priority = %v", err)
	}
	_, err := e.request("rq-r", "TOTE-9", asrsLoc, "ST1", tr.PurposePick)
	rejected(t, err, tr.ReasonUnknownContainer)
	e.stored("TOTE-9")
	_, err = e.request("rq-r", "TOTE-9", asrsLoc, "ST1", tr.PurposePick)
	rejected(t, err, tr.ReasonUnknownContainer)
	if n := len(e.events.All()); n != 2 {
		t.Fatalf("a replayed rejection published again: %d events", n)
	}
}

func TestPurposeAndFieldRulesAreFinalRequestErrors(t *testing.T) {
	e := newEnv(t)
	cases := []struct {
		src, dst string
		p        tr.Purpose
	}{
		{asrsLoc, pack, tr.PurposePick},
		{"ST1", "ST1", tr.PurposeOutbound},
		{asrsLoc, asrsLoc, tr.PurposeReturn},
		{decant, "ST1", tr.PurposeReplenish},
		{asrsLoc, pack, tr.PurposeOutbound},
		{"ST1", pack, tr.PurposePutaway},
	}
	for _, c := range cases {
		if _, err := e.request("rq", "TOTE-1", c.src, c.dst, c.p); !errors.Is(err, usecases.ErrInvalidRequest) {
			t.Fatalf("%s %s->%s = %v, want ErrInvalidRequest", c.p, c.src, c.dst, err)
		}
	}
	if _, err := e.request("", "TOTE-1", decant, asrsLoc, tr.PurposePutaway); !errors.Is(err, usecases.ErrInvalidRequest) {
		t.Fatalf("no id = %v", err)
	}
	if len(e.events.All()) != 0 {
		t.Fatal("a request error published")
	}
}

// TestCancellationAnswersDependOnTheLeg is the brief's test (task-9-brief Step 2) split into t.Helper steps to
// pass cyclop 15 (Task 9 report D3): every assertion is byte-identical and runs in the original order; only
// the second `v, out, err =` became `:=` in its own helper.
func TestCancellationAnswersDependOnTheLeg(t *testing.T) {
	e := newEnv(t)
	assertAnInFlightLegIsAskedToCancelOnce(t, e)
	assertATransportWaitingForAStationIsCancelledNow(t, e)
	assertEndedUnknownAndRejectedRequestsRefuse(t, e)
	assertOnlyTheWaitingTransportFailed(t, e)
}

func assertAnInFlightLegIsAskedToCancelOnce(t *testing.T, e *env) {
	t.Helper()
	e.mustAccept("rq-o", "TOTE-1", "ST1", pack, tr.PurposeOutbound)
	v, out, err := e.svc.CancelTransport(ctx, "rq-o")
	if err != nil || out != usecases.CancelAsked || v.Status != usecases.ViewCancelRequested {
		t.Fatalf("in flight = %+v %s %v", v, out, err)
	}
	if c := e.pending(command.CancelLeg); len(c) != 1 || c[0].Key != "cancel-"+v.OrderID+".1.1" {
		t.Fatalf("cancel commands = %+v", c)
	}
	if _, out, _ := e.svc.CancelTransport(ctx, "rq-o"); out != usecases.CancelAsked || len(e.pending(command.CancelLeg)) != 1 {
		t.Fatal("a second cancellation queued another command")
	}
}

func assertATransportWaitingForAStationIsCancelledNow(t *testing.T, e *env) {
	t.Helper()
	for i, c := range []string{"TOTE-2", "TOTE-3", "TOTE-4"} {
		e.mustAccept("rq-s"+string(rune('a'+i)), c, decant, "ST1", tr.PurposePick)
	}
	v, out, err := e.svc.CancelTransport(ctx, "rq-sc")
	if err != nil || out != usecases.CancelledNow || v.Status != usecases.ViewFailed || v.Reason != "CANCELLED" || v.ContainerLocation != decant {
		t.Fatalf("waiting for a full station = %+v %s %v", v, out, err)
	}
}

func assertEndedUnknownAndRejectedRequestsRefuse(t *testing.T, e *env) {
	t.Helper()
	if _, _, err := e.svc.CancelTransport(ctx, "rq-sc"); !errors.Is(err, usecases.ErrTransportEnded) {
		t.Fatalf("ended = %v", err)
	}
	if _, _, err := e.svc.CancelTransport(ctx, "rq-none"); !errors.Is(err, usecases.ErrRequestNotFound) {
		t.Fatalf("unknown = %v", err)
	}
	_, _ = e.request("rq-rej", "TOTE-9", asrsLoc, "ST1", tr.PurposePick)
	if _, _, err := e.svc.CancelTransport(ctx, "rq-rej"); !errors.Is(err, usecases.ErrTransportEnded) {
		t.Fatalf("rejected = %v", err)
	}
}

func assertOnlyTheWaitingTransportFailed(t *testing.T, e *env) {
	t.Helper()
	failed := 0
	for _, typ := range e.types() {
		if typ == "transportorder.TransportFailed" {
			failed++
		}
	}
	if got := e.types(); failed != 1 || got[len(got)-1] != "transportrequest.TransportRejected" {
		t.Fatalf("events = %v (one TransportFailed: the cancelled waiting transport)", got)
	}
}

func TestAClarificationIsResolvedWhereTheOperatorSays(t *testing.T) {
	e := newEnv(t)
	held := ct.New("TOTE-1", t0)
	held.MoveTo(ct.Position{Kind: ct.AtClarification, Code: clrLoc}, t0)
	held.SendToClarification(ct.Clarification{Reason: ct.ReasonMismatch}, t0)
	_ = e.tracks.Save(ctx, held)
	v, err := e.svc.ResolveClarification(ctx, "TOTE-1", decant)
	if err != nil || v.Track.Clarification != nil || v.Track.Position != (ct.Position{Kind: ct.AtHandoff, Code: decant}) || len(v.History) != 2 {
		t.Fatalf("resolved = %+v %v", v, err)
	}
	if _, err := e.svc.ResolveClarification(ctx, "TOTE-1", ""); !errors.Is(err, usecases.ErrNotInClarification) {
		t.Fatalf("again = %v", err)
	}
	if _, err := e.svc.ResolveClarification(ctx, "TOTE-404", ""); !errors.Is(err, usecases.ErrTrackNotFound) {
		t.Fatalf("unknown = %v", err)
	}
	held2 := ct.New("TOTE-2", t0)
	held2.SendToClarification(ct.Clarification{Reason: ct.ReasonNoRead}, t0)
	_ = e.tracks.Save(ctx, held2)
	if _, err := e.svc.ResolveClarification(ctx, "TOTE-2", "WH1-NOWHERE"); !errors.Is(err, usecases.ErrUnknownLocation) {
		t.Fatalf("unknown location = %v", err)
	}
}

func TestQueriesAnswerNotFoundAndDefaults(t *testing.T) {
	e := newEnv(t)
	if _, err := e.svc.Transport(ctx, "nope"); !errors.Is(err, usecases.ErrRequestNotFound) {
		t.Fatalf("Transport = %v", err)
	}
	if _, err := e.svc.Track(ctx, "nope"); !errors.Is(err, usecases.ErrTrackNotFound) {
		t.Fatalf("Track = %v", err)
	}
	if _, err := e.svc.Station(ctx, "ST9"); !errors.Is(err, usecases.ErrStationNotFound) {
		t.Fatalf("Station = %v", err)
	}
	q, err := e.svc.Station(ctx, "ST1")
	if err != nil || q.BufferLimit != 2 || len(q.Occupants) != 0 {
		t.Fatalf("an empty station = %+v %v", q, err)
	}
	if subs, _ := e.svc.Subsystems(ctx); len(subs) != 0 {
		t.Fatalf("subsystems = %+v", subs)
	}
	if _, err := usecases.NewService(e.svc.Layout(), usecases.DefaultConfig(), usecases.Deps{}); err == nil {
		t.Fatal("NewService without dependencies")
	}
}
