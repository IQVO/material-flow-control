package usecases_test

import (
	"errors"
	"testing"
	"time"

	"github.com/IQVO/material-flow-control/internal/adapters/outbound/memory"
	"github.com/IQVO/material-flow-control/internal/application/command"
	"github.com/IQVO/material-flow-control/internal/application/facts"
	"github.com/IQVO/material-flow-control/internal/application/usecases"
	"github.com/IQVO/material-flow-control/internal/domain/capacity"
	ct "github.com/IQVO/material-flow-control/internal/domain/containertrack"
	"github.com/IQVO/material-flow-control/internal/domain/transportorder"
	tr "github.com/IQVO/material-flow-control/internal/domain/transportrequest"
)

const (
	typeStored    = "com.warehouse.wes.asrs-control.storagejob.ContainerStored"
	typePresented = "com.warehouse.wes.asrs-control.storagejob.ContainerPresentedAtPort"
	typeJobFailed = "com.warehouse.wes.asrs-control.storagejob.StorageJobFailed"
	typeAsrsCap   = "com.warehouse.wes.asrs-control.asrs.AsrsCapacityChanged"
	typeUnit      = "com.warehouse.wes.asrs-control.equipmentunit.EquipmentStateChanged"
	typeAssigned  = "com.warehouse.wes.amr-fleet-control.mission.LegAssigned"
	typePicked    = "com.warehouse.wes.amr-fleet-control.mission.ContainerPickedUp"
	typeCompleted = "com.warehouse.wes.amr-fleet-control.mission.LegCompleted"
	typeLegFailed = "com.warehouse.wes.amr-fleet-control.mission.LegFailed"
	typeFleetCap  = "com.warehouse.wes.amr-fleet-control.fleet.FleetCapacityChanged"
)

func (e *env) must(err error) {
	e.t.Helper()
	if err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) lastRef(kind command.Kind) string {
	e.t.Helper()
	p := e.pending(kind)
	if len(p) == 0 {
		e.t.Fatalf("no pending %s", kind)
	}
	return p[len(p)-1].LegRef
}

// done marks every pending command executed (the dispatcher is Task 11).
func (e *env) done() {
	for _, c := range e.pending(command.RequestLeg) {
		_ = e.cmds.Save(ctx, c.Done(t0))
	}
}

func (e *env) storedFact(ref, container, port string) {
	e.must(e.svc.OnContainerStored(ctx, e.id(), typeStored, facts.ContainerStored{JobID: "sj", AsrsID: "ASRS1", ContainerID: container, PortID: port, Reference: ref}))
}

func (e *env) presented(ref, container, port, scan string) {
	e.must(e.svc.OnContainerPresented(ctx, e.id(), typePresented, facts.ContainerPresented{JobID: "sj", AsrsID: "ASRS1", ContainerID: container,
		PortID: port, Reference: ref, ScanResult: scan, ScannedContainerID: "TOTE-X"}))
}

func (e *env) pickedUp(ref, container, from string) {
	e.must(e.svc.OnContainerPickedUp(ctx, e.id(), typePicked, facts.ContainerPickedUp{LegID: "leg", FleetID: "AMR1", ContainerID: container, From: from, Reference: ref, Robot: "EMU/R1"}))
}

func (e *env) completed(ref, container, to string) {
	e.must(e.svc.OnLegCompleted(ctx, e.id(), typeCompleted, facts.LegCompleted{LegID: "leg", FleetID: "AMR1", ContainerID: container, To: to, Reference: ref, Robot: "EMU/R1"}))
}

func (e *env) legFailed(ref, container, reason string, onRobot bool) {
	e.must(e.svc.OnLegFailed(ctx, e.id(), typeLegFailed, facts.LegFailed{LegID: "leg", FleetID: "AMR1", ContainerID: container, Reference: ref,
		Robot: "EMU/R1", Reason: reason, ContainerOnRobot: onRobot}))
}

func (e *env) position(container string) ct.Position {
	e.t.Helper()
	track, err := e.tracks.Get(ctx, container)
	if err != nil {
		e.t.Fatal(err)
	}
	return track.Position()
}

func (e *env) last() any {
	all := e.events.All()
	for i := len(all) - 1; i >= 0; i-- {
		if _, isCap := all[i].(capacity.SubsystemCapacityChanged); !isCap {
			return all[i]
		}
	}
	return nil
}

// TestGoodsToPersonThenReturnWithoutALedgerFact is the plan 04 Task 10 brief's test split for cyclop (max 15): its three
// phases moved verbatim, in order, into assertThePickIsPresentedThenPickedUp, assertTheArrivalCompletesThePick and
// assertTheReturnIsStoredWithoutALedgerFact; every assertion is byte-identical to the brief (Task 10 report D2).
func TestGoodsToPersonThenReturnWithoutALedgerFact(t *testing.T) {
	e := newEnv(t)
	move := assertThePickIsPresentedThenPickedUp(t, e)
	assertTheArrivalCompletesThePick(t, e, move)
	assertTheReturnIsStoredWithoutALedgerFact(t, e)
}

func assertThePickIsPresentedThenPickedUp(t *testing.T, e *env) string {
	t.Helper()
	e.storedFact("", "TOTE-1", "IN1")
	if p := e.position("TOTE-1"); p != (ct.Position{Kind: ct.InAsrs, Code: asrsLoc}) {
		t.Fatalf("a stored container is in the AS/RS: %+v", p)
	}
	pick := e.mustAccept("rq-1", "TOTE-1", asrsLoc, "ST1", tr.PurposePick)
	retrieve := e.lastRef(command.RequestLeg)
	e.done()
	e.presented(retrieve, "TOTE-1", "OUT1", "READ")
	if p := e.position("TOTE-1"); p != (ct.Position{Kind: ct.AtPort, Code: out1Loc}) {
		t.Fatalf("at the port: %+v", p)
	}
	move := e.lastRef(command.RequestLeg)
	if move != pick.OrderID+".2.1" {
		t.Fatalf("the move starts only after the presentation: %s", move)
	}
	e.must(e.svc.OnLegAssigned(ctx, e.id(), typeAssigned, facts.LegAssigned{Reference: move, Robot: "EMU/R1", FleetID: "AMR1"}))
	e.pickedUp(move, "TOTE-1", "ASRS1-OUT1")
	if c := e.pending(command.ConfirmPickup); len(c) != 1 || c[0].LegRef != move || c[0].Key != "pickup-"+move {
		t.Fatalf("pickup confirmation = %+v", c)
	}
	if p := e.position("TOTE-1"); p != ct.OnRobotOf("AMR1", "EMU/R1") {
		t.Fatalf("on the robot: %+v", p)
	}
	return move
}

func assertTheArrivalCompletesThePick(t *testing.T, e *env, move string) {
	t.Helper()
	e.completed(move, "TOTE-1", "ST1")
	evs := e.events.All()
	var arrived transportorder.ContainerArrivedAtStation
	var completed transportorder.TransportCompleted
	for _, ev := range evs {
		switch v := ev.(type) {
		case transportorder.ContainerArrivedAtStation:
			arrived = v
		case transportorder.TransportCompleted:
			completed = v
		}
	}
	if arrived.StationID != "ST1" || arrived.QueuePosition != 1 || completed.RequestID != "rq-1" || completed.Destination != "ST1" {
		t.Fatalf("arrival = %+v, completion = %+v", arrived, completed)
	}
	if v, _ := e.svc.Transport(ctx, "rq-1"); v.Status != usecases.ViewCompleted || v.LegsDone != 2 || v.ContainerLocation != "ST1" {
		t.Fatalf("view = %+v", v)
	}
}

func assertTheReturnIsStoredWithoutALedgerFact(t *testing.T, e *env) {
	t.Helper()
	e.mustAccept("rq-2", "TOTE-1", "ST1", asrsLoc, tr.PurposeReturn)
	back := e.lastRef(command.RequestLeg)
	e.done()
	e.pickedUp(back, "TOTE-1", "ST1")
	//nolint:staticcheck // S1009: the brief's assertion, kept byte-identical (Task 10 report D3)
	if q, _ := e.svc.Station(ctx, "ST1"); q.Occupants != nil && len(q.Occupants) != 0 {
		t.Fatalf("the container left the station: %+v", q)
	}
	e.completed(back, "TOTE-1", "ASRS1-IN1")
	store := e.lastRef(command.RequestLeg)
	if store == back {
		t.Fatal("no store after the drop at the input port")
	}
	e.storedFact(store, "TOTE-1", "IN1")
	if v, _ := e.svc.Transport(ctx, "rq-2"); v.Status != usecases.ViewCompleted {
		t.Fatalf("return = %+v", v)
	}
	if m := e.metrics.Ended(); len(m) != 2 || m[0].Purpose != "PICK" || m[1].Purpose != "RETURN" || m[1].Outcome != "completed" {
		t.Fatalf("metrics = %+v", m)
	}
}

// TestANoReadSendsTheContainerToClarificationAndFreesThePort is the plan 04 Task 10 brief's test split for cyclop
// (max 15): its tail moved verbatim, in order, into assertTheClarificationMoveFreesThePortAndHolds; every assertion is
// byte-identical to the brief (Task 10 report D2).
func TestANoReadSendsTheContainerToClarificationAndFreesThePort(t *testing.T) {
	e := newEnv(t)
	e.stored("TOTE-1")
	e.mustAccept("rq-1", "TOTE-1", asrsLoc, "ST1", tr.PurposePick)
	retrieve := e.lastRef(command.RequestLeg)
	e.done()
	e.presented(retrieve, "TOTE-1", "OUT1", "NO_READ")
	var sent ct.ContainerSentToClarification
	var failed transportorder.TransportFailed
	order := []string{}
	for _, ev := range e.events.All() {
		switch v := ev.(type) {
		case ct.ContainerSentToClarification:
			sent, order = v, append(order, "sent")
		case transportorder.TransportFailed:
			failed, order = v, append(order, "failed")
		}
	}
	if !equal(order, []string{"sent", "failed"}) || sent.Reason != ct.ReasonNoRead || sent.DetectedAt != out1Loc || sent.ClarificationPoint != clrLoc ||
		sent.RequestID != "rq-1" || failed.Reason != transportorder.FailClarification || failed.ContainerLocation != out1Loc {
		t.Fatalf("sent = %+v, failed = %+v, order %v", sent, failed, order)
	}
	assertTheClarificationMoveFreesThePortAndHolds(t, e)
}

func assertTheClarificationMoveFreesThePortAndHolds(t *testing.T, e *env) {
	t.Helper()
	internal := e.lastRef(command.RequestLeg)
	_, err := e.request("rq-2", "TOTE-1", out1Loc, pack, tr.PurposeOutbound)
	rejected(t, err, tr.ReasonContainerBusy)
	e.pickedUp(internal, "TOTE-1", "ASRS1-OUT1")
	if len(e.pending(command.ConfirmPickup)) != 1 {
		t.Fatal("the clarification move frees the port")
	}
	e.completed(internal, "TOTE-1", "CLR1")
	track, _ := e.tracks.Get(ctx, "TOTE-1")
	if c, held := track.Clarification(); !held || c.Reason != ct.ReasonNoRead || track.Position() != (ct.Position{Kind: ct.AtClarification, Code: clrLoc}) || track.ActiveOrderID() != "" {
		t.Fatalf("track = %+v", track.Snapshot())
	}
}

func TestABlockedRobotIsRetriedTwiceThenTheTransportFails(t *testing.T) {
	e := newEnv(t)
	v := e.mustAccept("rq-1", "TOTE-1", "ST1", pack, tr.PurposeOutbound)
	ref := e.lastRef(command.RequestLeg)
	for attempt := 2; attempt <= 3; attempt++ {
		e.done()
		e.legFailed(ref, "TOTE-1", "ROBOT_BLOCKED", false)
		ref = e.lastRef(command.RequestLeg)
		if want := v.OrderID + ".1." + string(rune('0'+attempt)); ref != want {
			t.Fatalf("attempt %d ref = %s, want %s", attempt, ref, want)
		}
	}
	e.done()
	e.legFailed(ref, "TOTE-1", "ROBOT_BLOCKED", false)
	f, ok := e.last().(transportorder.TransportFailed)
	if !ok || f.Reason != transportorder.FailSubsystemFault || f.SubsystemReason != "ROBOT_BLOCKED" || f.ContainerLocation != "ST1" {
		t.Fatalf("last = %+v", e.last())
	}
	if len(e.pending(command.RequestLeg)) != 0 {
		t.Fatal("a fourth attempt was queued")
	}
}

func TestAContainerOnAFailedRobotIsAnAnomalyAndNoReadAtThePickIsClarification(t *testing.T) {
	e := newEnv(t)
	e.mustAccept("rq-1", "TOTE-1", "ST1", pack, tr.PurposeOutbound)
	ref := e.lastRef(command.RequestLeg)
	e.pickedUp(ref, "TOTE-1", "ST1")
	e.legFailed(ref, "TOTE-1", "ROBOT_FAULT", true)
	f, _ := e.last().(transportorder.TransportFailed)
	if f.Reason != transportorder.FailSubsystemFault || f.ContainerLocation != "" {
		t.Fatalf("failed = %+v (the container is on a robot: no location)", f)
	}
	track, _ := e.tracks.Get(ctx, "TOTE-1")
	if c, held := track.Clarification(); !held || c.Reason != ct.ReasonOnFailedRobot {
		t.Fatalf("track = %+v", track.Snapshot())
	}
	e.mustAccept("rq-2", "TOTE-2", decant, pack, tr.PurposeOutbound)
	ref2 := e.lastRef(command.RequestLeg)
	e.legFailed(ref2, "TOTE-2", "PICK_FAILED", false)
	if f, _ := e.last().(transportorder.TransportFailed); f.Reason != transportorder.FailClarification || f.SubsystemReason != "PICK_FAILED" {
		t.Fatalf("pick failed = %+v", e.last())
	}
}

func TestAFailedStoreIsReplannedThroughTheOtherInputPortOnce(t *testing.T) {
	e := newEnv(t)
	e.mustAccept("rq-1", "TOTE-1", decant, asrsLoc, tr.PurposePutaway)
	move := e.lastRef(command.RequestLeg)
	e.done()
	e.completed(move, "TOTE-1", "ASRS1-IN1")
	store := e.lastRef(command.RequestLeg)
	e.done()
	fail := func(ref string) {
		e.must(e.svc.OnStorageJobFailed(ctx, e.id(), typeJobFailed, facts.StorageJobFailed{AsrsID: "ASRS1", Kind: "STORE", ContainerID: "TOTE-1",
			PortID: "IN1", Reference: ref, Reason: "EQUIPMENT_FAULT"}))
	}
	fail(store)
	replan := e.lastRef(command.RequestLeg)
	o, _ := e.svc.OrderDetail(ctx, "rq-1")
	leg := o.Legs[len(o.Legs)-2]
	if replan != leg.Ref || leg.From.PortID != "IN1" || leg.To.PortID != "IN2" {
		t.Fatalf("re-plan leg = %+v (ref %s)", leg, replan)
	}
	e.done()
	e.completed(replan, "TOTE-1", "ASRS1-IN2")
	second := e.lastRef(command.RequestLeg)
	e.done()
	fail(second)
	if f, _ := e.last().(transportorder.TransportFailed); f.Reason != transportorder.FailSubsystemFault || f.SubsystemReason != "EQUIPMENT_FAULT" {
		t.Fatalf("after the one re-plan = %+v", e.last())
	}
}

func TestAConfirmedCancellationEndsTheTransportCancelled(t *testing.T) {
	e := newEnv(t)
	e.mustAccept("rq-1", "TOTE-1", "ST1", pack, tr.PurposeOutbound)
	ref := e.lastRef(command.RequestLeg)
	if _, out, err := e.svc.CancelTransport(ctx, "rq-1"); err != nil || out != usecases.CancelAsked {
		t.Fatalf("cancel = %s %v", out, err)
	}
	e.legFailed(ref, "TOTE-1", "CANCELLED", false)
	if f, _ := e.last().(transportorder.TransportFailed); f.Reason != transportorder.FailCancelled || f.ContainerLocation != "ST1" {
		t.Fatalf("cancelled = %+v", e.last())
	}
}

func TestStationAdmissionFollowsPriorityWhenASlotFrees(t *testing.T) {
	e := newEnv(t)
	e.mustAccept("rq-a", "TOTE-A", decant, "ST1", tr.PurposePick)
	e.mustAccept("rq-b", "TOTE-B", decant, "ST1", tr.PurposePick)
	e.mustAccept("rq-n", "TOTE-N", decant, "ST1", tr.PurposePick)
	_, err := e.svc.RequestTransport(ctx, tr.Request{ID: "rq-u", ContainerID: "TOTE-U", Source: decant, Destination: "ST1",
		Purpose: tr.PurposePick, Priority: tr.PriorityUrgent, DueBy: t0.Add(2 * time.Hour)})
	e.must(err)
	if q, _ := e.svc.Station(ctx, "ST1"); len(q.Occupants) != 2 || len(q.Waiting) != 2 || q.Waiting[0].RequestID != "rq-u" {
		t.Fatalf("queue = %+v", q)
	}
	a, _ := e.svc.OrderDetail(ctx, "rq-a")
	e.completed(a.Legs[0].Ref, "TOTE-A", "ST1")
	e.mustAccept("rq-a2", "TOTE-A", "ST1", pack, tr.PurposeOutbound)
	out := e.lastRef(command.RequestLeg)
	e.pickedUp(out, "TOTE-A", "ST1")
	q, _ := e.svc.Station(ctx, "ST1")
	if len(q.Waiting) != 1 || q.Waiting[0].RequestID != "rq-n" || q.Occupants[len(q.Occupants)-1].RequestID != "rq-u" {
		t.Fatalf("URGENT is admitted first: %+v", q)
	}
	var degraded, available int
	for _, ev := range e.events.All() {
		if c, ok := ev.(capacity.SubsystemCapacityChanged); ok && c.Kind == capacity.KindStation {
			if c.Status == capacity.StatusDegraded {
				degraded++
			} else {
				available++
			}
		}
	}
	if degraded == 0 || available == 0 {
		t.Fatalf("station capacity events: %d degraded, %d available", degraded, available)
	}
}

// TestCapacityFactsArePublishedOnChangeAndGateAcceptance is the plan 04 Task 10 brief's test split for cyclop (max 15):
// its two phases moved verbatim, in order, into assertFleetSnapshotsArePublishedOnChange and
// assertCapacityGatesAcceptanceAndMapsEveryFact (the fleet closure is passed to both); every assertion is
// byte-identical to the brief (Task 10 report D2).
func TestCapacityFactsArePublishedOnChangeAndGateAcceptance(t *testing.T) {
	e := newEnv(t)
	fleet := func(online, available int) {
		e.must(e.svc.OnFleetCapacityChanged(ctx, e.id(), typeFleetCap, facts.FleetCapacity{FleetID: "AMR1", RobotsTotal: 3, RobotsOnline: online, RobotsAvailable: available}))
	}
	assertFleetSnapshotsArePublishedOnChange(t, e, fleet)
	assertCapacityGatesAcceptanceAndMapsEveryFact(t, e, fleet)
}

func assertFleetSnapshotsArePublishedOnChange(t *testing.T, e *env, fleet func(online, available int)) {
	t.Helper()
	fleet(3, 3)
	fleet(1, 1)
	fleet(1, 1)
	var snaps []capacity.SubsystemCapacityChanged
	for _, ev := range e.events.All() {
		snaps = append(snaps, ev.(capacity.SubsystemCapacityChanged))
	}
	if len(snaps) != 2 || snaps[0].Status != capacity.StatusAvailable || snaps[0].CapacityPerHour != 90 ||
		snaps[1].Status != capacity.StatusDegraded || snaps[1].UnitsAvailable != 1 || snaps[1].Subject() != "AMR_FLEET/AMR1" {
		t.Fatalf("fleet snapshots = %+v", snaps)
	}
}

func assertCapacityGatesAcceptanceAndMapsEveryFact(t *testing.T, e *env, fleet func(online, available int)) {
	t.Helper()
	fleet(0, 0)
	_, err := e.request("rq-1", "TOTE-1", "ST1", pack, tr.PurposeOutbound)
	rejected(t, err, tr.ReasonSubsystemUnavailable)
	before := len(e.events.All())
	e.must(e.svc.OnFleetCapacityChanged(ctx, e.id(), typeFleetCap, facts.FleetCapacity{FleetID: "AMR1", LegsQueued: 2, LegsActive: 1,
		At: t0.Add(-time.Hour)}))
	last, ok := e.events.All()[len(e.events.All())-1].(capacity.SubsystemCapacityChanged)
	if len(e.events.All()) != before+1 || !ok || last.UnitsTotal != 0 || last.OpenWork != 3 || last.Status != capacity.StatusUnavailable ||
		!last.MeasuredAt.Equal(t0) {
		t.Fatalf("robots_total 0 must be accepted, open work = queued + active, measured_at = our clock (not changed_at): %+v", last)
	}
	e.must(e.svc.OnAsrsCapacityChanged(ctx, e.id(), typeAsrsCap, facts.AsrsCapacity{AsrsID: "ASRS1", UnitsTotal: 2, UnitsAvailable: 1, OpenJobs: 4}))
	if subs, _ := e.svc.Subsystems(ctx); len(subs) != 2 || subs[0].Kind != capacity.KindAmrFleet || subs[1].CapacityPerHour != 60 {
		t.Fatalf("subsystems = %+v", subs)
	}
	e.must(e.svc.OnEquipmentStateChanged(ctx, e.id(), typeUnit, facts.EquipmentState{AsrsID: "ASRS1", UnitID: "A1", Aisle: 1, State: "HELD", PreviousState: "IDLE"}))
	if u, _ := e.svc.EquipmentUnits(ctx, "ASRS1"); len(u) != 1 || u[0].State != "HELD" {
		t.Fatalf("units = %+v", u)
	}
}

func TestADuplicateEventIDIsAppliedOnceAndAnUnknownReferenceChangesNoOrder(t *testing.T) {
	e := newEnv(t)
	e.mustAccept("rq-1", "TOTE-1", "ST1", pack, tr.PurposeOutbound)
	ref := e.lastRef(command.RequestLeg)
	f := facts.LegFailed{FleetID: "AMR1", ContainerID: "TOTE-1", Reference: ref, Reason: "ROBOT_BLOCKED"}
	e.must(e.svc.OnLegFailed(ctx, "dup", typeLegFailed, f))
	e.must(e.svc.OnLegFailed(ctx, "dup", typeLegFailed, f))
	if o, _ := e.svc.OrderDetail(ctx, "rq-1"); o.Retries != 1 {
		t.Fatalf("retries = %d, want 1", o.Retries)
	}
	before := len(e.events.All())
	e.completed("to-unknown.1.1", "TOTE-9", "PK1")
	if len(e.events.All()) != before {
		t.Fatal("an unknown reference changed an order")
	}
	if p := e.position("TOTE-9"); p != (ct.Position{Kind: ct.AtHandoff, Code: pack}) {
		t.Fatalf("the physical fact still moves the track: %+v", p)
	}
}

func TestAFailedWriteRollsBackTheFactAndItsClaim(t *testing.T) {
	e := newEnv(t)
	e.mustAccept("rq-1", "TOTE-1", "ST1", pack, tr.PurposeOutbound)
	ref := e.lastRef(command.RequestLeg)
	e.store.FailNext("events")
	err := e.svc.OnLegCompleted(ctx, "once", typeCompleted, facts.LegCompleted{FleetID: "AMR1", ContainerID: "TOTE-1", To: "PK1", Reference: ref})
	if !errors.Is(err, memory.ErrInjected) {
		t.Fatalf("err = %v", err)
	}
	if p := e.position("TOTE-1"); p.Code != "ST1" {
		t.Fatalf("rolled-back move: %+v", p)
	}
	e.must(e.svc.OnLegCompleted(ctx, "once", typeCompleted, facts.LegCompleted{FleetID: "AMR1", ContainerID: "TOTE-1", To: "PK1", Reference: ref}))
	if v, _ := e.svc.Transport(ctx, "rq-1"); v.Status != usecases.ViewCompleted {
		t.Fatalf("the redelivery applies: %+v", v)
	}
}
