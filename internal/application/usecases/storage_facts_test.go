package usecases_test

import (
	"testing"

	"github.com/IQVO/material-flow-control/internal/application/command"
	"github.com/IQVO/material-flow-control/internal/application/facts"
	"github.com/IQVO/material-flow-control/internal/application/usecases"
	ct "github.com/IQVO/material-flow-control/internal/domain/containertrack"
	"github.com/IQVO/material-flow-control/internal/domain/transportorder"
	tr "github.com/IQVO/material-flow-control/internal/domain/transportrequest"
)

// carry (Task 10 review): the storageJobFailed arms and the unknown-subsystem skips no brief test executes.

func (e *env) jobFailed(kind, port, ref, reason string) {
	e.must(e.svc.OnStorageJobFailed(ctx, e.id(), typeJobFailed, facts.StorageJobFailed{AsrsID: "ASRS1", Kind: kind, ContainerID: "TOTE-1",
		PortID: port, Reference: ref, Reason: reason}))
}

// putawayAtIN1 accepts a putaway from the decant station and completes its MOVE at IN1; it returns the STORE's ref.
func (e *env) putawayAtIN1() string {
	e.t.Helper()
	e.mustAccept("rq-1", "TOTE-1", decant, asrsLoc, tr.PurposePutaway)
	move := e.lastRef(command.RequestLeg)
	e.done()
	e.completed(move, "TOTE-1", "ASRS1-IN1")
	store := e.lastRef(command.RequestLeg)
	e.done()
	return store
}

// carry (Task 10 review): asrs-control's CANCELLED ends the transport CANCELLED only after our cancellation;
// unasked it is a SUBSYSTEM_FAULT carrying the bare reason. Either way the container stays in the AS/RS.
func TestAStorageJobCancelledEndsCancelledOnlyWhenWeAsked(t *testing.T) {
	for _, asked := range []bool{true, false} {
		e := newEnv(t)
		e.stored("TOTE-1")
		e.mustAccept("rq-1", "TOTE-1", asrsLoc, "ST1", tr.PurposePick)
		ref := e.lastRef(command.RequestLeg)
		if asked {
			if _, out, err := e.svc.CancelTransport(ctx, "rq-1"); err != nil || out != usecases.CancelAsked {
				t.Fatalf("cancel = %s %v", out, err)
			}
		}
		e.jobFailed("RETRIEVE", "OUT1", ref, "CANCELLED")
		want := transportorder.TransportFailed{Reason: transportorder.FailSubsystemFault, SubsystemReason: "CANCELLED", ContainerLocation: asrsLoc}
		if asked {
			want = transportorder.TransportFailed{Reason: transportorder.FailCancelled, ContainerLocation: asrsLoc}
		}
		f, ok := e.last().(transportorder.TransportFailed)
		if !ok || f.Reason != want.Reason || f.SubsystemReason != want.SubsystemReason || f.ContainerLocation != want.ContainerLocation {
			t.Fatalf("asked %v: last = %+v, want %+v", asked, e.last(), want)
		}
		if p := e.position("TOTE-1"); p != (ct.Position{Kind: ct.InAsrs, Code: asrsLoc}) {
			t.Fatalf("asked %v: position = %+v", asked, p)
		}
	}
}

// carry (Task 10 review): CONTAINER_NOT_AT_PORT on a STORE holds the container where the STORE started (the input
// port), fails the transport CLARIFICATION and builds no clarification move (an input port is not an output port).
func TestAContainerNotAtTheInputPortIsHeldWhereTheStoreStarted(t *testing.T) {
	e := newEnv(t)
	store := e.putawayAtIN1()
	before := len(e.events.All())
	e.jobFailed("STORE", "IN1", store, "CONTAINER_NOT_AT_PORT")
	track, err := e.tracks.Get(ctx, "TOTE-1")
	e.must(err)
	if c, held := track.Clarification(); !held || c.Reason != ct.ReasonNotAtPort || c.DetectedAt != in1Loc {
		t.Fatalf("track = %+v, want held %s at %s", track.Snapshot(), ct.ReasonNotAtPort, in1Loc)
	}
	evs := e.events.All()[before:]
	if len(evs) != 2 {
		t.Fatalf("events = %+v, want ContainerSentToClarification then TransportFailed", evs)
	}
	if sent, ok := evs[0].(ct.ContainerSentToClarification); !ok || sent.Reason != ct.ReasonNotAtPort || sent.DetectedAt != in1Loc || sent.ClarificationPoint != "" {
		t.Fatalf("first event = %+v", evs[0])
	}
	f, ok := evs[1].(transportorder.TransportFailed)
	if !ok || f.Reason != transportorder.FailClarification || f.SubsystemReason != "CONTAINER_NOT_AT_PORT" || f.ContainerLocation != in1Loc {
		t.Fatalf("second event = %+v", evs[1])
	}
	if p := e.pending(command.RequestLeg); len(p) != 0 {
		t.Fatalf("a clarification move was planned: %+v", p)
	}
}

// carry (Task 10 review): STORAGE_MAP_MISMATCH on a RETRIEVE holds the container in the AS/RS, fails the transport
// CLARIFICATION at the AS/RS location, and a new request for it is refused CONTAINER_BUSY.
func TestAStorageMapMismatchHoldsTheContainerInTheAsrs(t *testing.T) {
	e := newEnv(t)
	e.stored("TOTE-1")
	e.mustAccept("rq-1", "TOTE-1", asrsLoc, "ST1", tr.PurposePick)
	e.jobFailed("RETRIEVE", "OUT1", e.lastRef(command.RequestLeg), "STORAGE_MAP_MISMATCH")
	track, err := e.tracks.Get(ctx, "TOTE-1")
	e.must(err)
	if c, held := track.Clarification(); !held || c.Reason != ct.ReasonStorageMapMismatch || c.DetectedAt != asrsLoc {
		t.Fatalf("track = %+v, want held %s at %s", track.Snapshot(), ct.ReasonStorageMapMismatch, asrsLoc)
	}
	if p := track.Position(); p != (ct.Position{Kind: ct.InAsrs, Code: asrsLoc}) {
		t.Fatalf("position = %+v", p)
	}
	f, ok := e.last().(transportorder.TransportFailed)
	if !ok || f.Reason != transportorder.FailClarification || f.SubsystemReason != "STORAGE_MAP_MISMATCH" || f.ContainerLocation != asrsLoc {
		t.Fatalf("last = %+v", e.last())
	}
	_, err = e.request("rq-2", "TOTE-1", asrsLoc, "ST1", tr.PurposePick)
	rejected(t, err, tr.ReasonContainerBusy)
}

// carry (Task 10 review): a fact naming an AS/RS, port or fleet the layout does not know is skipped: no track,
// no event.
func TestFactsNamingAnUnknownSubsystemAreSkipped(t *testing.T) {
	e := newEnv(t)
	e.must(e.svc.OnContainerStored(ctx, e.id(), typeStored, facts.ContainerStored{JobID: "sj", AsrsID: "ASRS9", ContainerID: "TOTE-1", PortID: "IN1"}))
	e.must(e.svc.OnContainerPresented(ctx, e.id(), typePresented, facts.ContainerPresented{JobID: "sj", AsrsID: "ASRS1", ContainerID: "TOTE-1",
		PortID: "OUT9", ScanResult: "READ"}))
	e.must(e.svc.OnAsrsCapacityChanged(ctx, e.id(), typeAsrsCap, facts.AsrsCapacity{AsrsID: "ASRS9", UnitsTotal: 2, UnitsAvailable: 2}))
	e.must(e.svc.OnFleetCapacityChanged(ctx, e.id(), typeFleetCap, facts.FleetCapacity{FleetID: "AMR9", RobotsTotal: 2, RobotsOnline: 2, RobotsAvailable: 2}))
	if _, err := e.tracks.Get(ctx, "TOTE-1"); err == nil {
		t.Fatal("a skipped fact created a track")
	}
	if n := len(e.events.All()); n != 0 {
		t.Fatalf("skipped facts published %d events: %v", n, e.types())
	}
}
