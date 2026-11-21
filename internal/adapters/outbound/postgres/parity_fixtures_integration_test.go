//go:build integration

package postgres_test

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/IQVO/material-flow-control/internal/adapters/outbound/memory"
	"github.com/IQVO/material-flow-control/internal/adapters/outbound/postgres"
	"github.com/IQVO/material-flow-control/internal/application/command"
	"github.com/IQVO/material-flow-control/internal/application/facts"
	"github.com/IQVO/material-flow-control/internal/application/idempotency"
	"github.com/IQVO/material-flow-control/internal/application/ports"
	"github.com/IQVO/material-flow-control/internal/domain/capacity"
	ct "github.com/IQVO/material-flow-control/internal/domain/containertrack"
	"github.com/IQVO/material-flow-control/internal/domain/layout"
	"github.com/IQVO/material-flow-control/internal/domain/routing"
	sq "github.com/IQVO/material-flow-control/internal/domain/stationqueue"
	to "github.com/IQVO/material-flow-control/internal/domain/transportorder"
	tr "github.com/IQVO/material-flow-control/internal/domain/transportrequest"
	"github.com/IQVO/material-flow-control/internal/testing/pgtest"
)

// The parity tests hold the Postgres repositories to the memory doubles, the behavioural oracle every use-case
// test runs on: each scenario runs on both and both must give the same answer.

// tn has a sub-microsecond tail: a snapshot keeps nanoseconds (JSON), as the memory doubles do.
var tn = t0.Add(123456789 * time.Nanosecond)

type repos struct {
	name     string
	requests ports.TransportRequests
	orders   ports.TransportOrders
	tracks   ports.ContainerTracks
	stations ports.StationQueues
	caps     ports.Capacities
	units    ports.EquipmentUnits
	commands ports.Commands
	idem     ports.IdempotencyStore
}

func bothRepos(t *testing.T) []repos {
	t.Helper()
	s, pool := memory.NewStore(), pgtest.Pool(t)
	return []repos{
		{name: "memory", requests: memory.NewRequests(s), orders: memory.NewOrders(s), tracks: memory.NewTracks(s),
			stations: memory.NewStations(s), caps: memory.NewCapacities(s), units: memory.NewUnits(s),
			commands: memory.NewCommands(s), idem: memory.NewIdempotency(s)},
		{name: "postgres", requests: postgres.NewRequests(pool), orders: postgres.NewOrders(pool), tracks: postgres.NewTracks(pool),
			stations: postgres.NewStations(pool), caps: postgres.NewCapacities(pool), units: postgres.NewUnits(pool),
			commands: postgres.NewCommands(pool), idem: postgres.NewIdempotencyStore(pool)},
	}
}

// oracle runs scenario on a fresh memory store and on a fresh database; both must return want.
func oracle(t *testing.T, name string, want any, scenario func(t *testing.T, r repos) any) {
	t.Helper()
	for _, r := range bothRepos(t) {
		if got := scenario(t, r); !reflect.DeepEqual(got, want) {
			t.Errorf("%s on %s:\n got %+v\nwant %+v", name, r.name, got, want)
		}
	}
}

func ok(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// must is called as must(f())(t): Go spreads a multi-value call only into a call that takes nothing else.
func must[T any](v T, err error) func(t *testing.T) T {
	return func(t *testing.T) T {
		t.Helper()
		ok(t, err)
		return v
	}
}

// nonZero fails for every zero field reachable from v. The fixtures below set every field, so a field a later
// task adds to a snapshot fails here until its fixture, and with it the round trip, covers it.
func nonZero(t *testing.T, path string, v reflect.Value) {
	t.Helper()
	switch {
	case v.Type() == reflect.TypeOf(time.Time{}):
		if v.Interface().(time.Time).IsZero() {
			t.Errorf("fixture field %s is zero", path)
		}
	case v.Kind() == reflect.Struct:
		for i := range v.NumField() {
			nonZero(t, path+"."+v.Type().Field(i).Name, v.Field(i))
		}
	case v.Kind() == reflect.Slice:
		if v.Len() == 0 {
			t.Errorf("fixture field %s is empty", path)
		}
		for i := range v.Len() {
			nonZero(t, fmt.Sprintf("%s[%d]", path, i), v.Index(i))
		}
	case v.Kind() == reflect.Pointer && !v.IsNil():
		nonZero(t, path, v.Elem())
	case v.IsZero():
		t.Errorf("fixture field %s is zero", path)
	}
}

func fullPlace(code string) layout.Place {
	return layout.Place{Kind: layout.PlaceHandoff, Code: code, AsrsID: "ASRS1", PortID: "OUT1", PortKind: layout.PortOutput,
		StationID: "ST1", HandoffPointID: "HP-" + code, HandoffKind: layout.HandoffPack}
}

func fullLeg(seq, attempt int, ref string) to.Leg {
	return to.Leg{Seq: seq, Attempt: attempt, Ref: ref, Subsystem: routing.SubsystemAmr, SubsystemID: "AMR1", Kind: routing.KindMove,
		From: fullPlace("WH1-RCV-DEC1"), To: fullPlace("WH1-PCK-PK1"), Status: to.LegPickedUp, SubsystemRef: "amr-leg-7",
		Robot: "EMU/R1", UpdatedAt: tn.Add(time.Minute), EndedAt: tn.Add(2 * time.Minute)}
}

func fullOrder(id string) to.Snapshot {
	return to.Snapshot{ID: id, RequestID: "rq-" + id, ContainerID: "TOTE-" + id, Source: fullPlace("WH1-RCV-DEC1"),
		Destination: fullPlace("WH1-PCK-PK1"), Purpose: tr.PurposeOutbound, Priority: tr.PriorityUrgent, DueBy: tn.Add(time.Hour),
		Internal: true, Legs: []to.Leg{fullLeg(1, 1, id+".1.1"), fullLeg(1, 2, id+".1.2")}, Status: to.StatusInProgress,
		Reason: to.FailTimeout, SubsystemReason: "robot-blocked", ContainerLocation: "WH1-RCV-DEC1", CancelRequested: true,
		CancelRefused: true, Retries: 2, Replans: 1, AcceptedAt: tn, EndedAt: tn.Add(3 * time.Minute), Version: 4}
}

func fullTrack() ct.Snapshot {
	port := "WH1-AUTO-ASRS-ASRS1-01-00-OUT1"
	return ct.Snapshot{ContainerID: "TOTE-9", Position: ct.Position{Kind: ct.AtPort, Code: port, Robot: "EMU/R1"},
		ActiveOrderID: "to-9", ActiveRequestID: "rq-9", Clarification: &ct.Clarification{Reason: ct.ReasonMismatch, OrderID: "to-9",
			RequestID: "rq-9", ScannedContainerID: "TOTE-X", DetectedAt: port, Since: tn}, UpdatedAt: tn, Version: 3}
}

func fullQueue() sq.Snapshot {
	return sq.Snapshot{StationID: "ST9", BufferLimit: 3,
		Occupants: []sq.Occupant{{OrderID: "to-1", RequestID: "rq-1", ContainerID: "TOTE-1", Seq: 4, AdmittedAt: tn}},
		Waiting: []sq.Entry{{OrderID: "to-2", RequestID: "rq-2", ContainerID: "TOTE-2", Priority: tr.PriorityUrgent,
			DueBy: tn.Add(time.Hour), ReadyAt: tn.Add(time.Minute)}},
		NextSeq: 5, Version: 2}
}

func fullCapacity() capacity.Snapshot {
	return capacity.Snapshot{Kind: capacity.KindStation, ID: "ST9", Status: capacity.StatusDegraded, UnitsTotal: 4, UnitsAvailable: 3,
		OpenWork: 2, CapacityPerHour: 90, BufferLimit: 3, Occupied: 1, QueueDepth: 2, MeasuredAt: tn}
}

func fullRecord() tr.Record {
	return tr.Record{Request: tr.Request{ID: "rq-9", ContainerID: "TOTE-9", Source: "WH1-RCV-DEC1", Destination: "WH1-PCK-PK1",
		Purpose: tr.PurposeOutbound, Priority: tr.PriorityUrgent, DueBy: tn.Add(time.Hour)},
		Outcome: tr.OutcomeRejected, OrderID: "to-9", Reason: tr.ReasonContainerBusy, Detail: "the container is held at clarification", At: tn}
}

func fullUnit() facts.EquipmentState {
	return facts.EquipmentState{AsrsID: "ASRS1", UnitID: "A1", Aisle: 2, State: "HELD", PreviousState: "IDLE", At: tn}
}

// fullCommand's times are whole microseconds: the command outbox keeps its times in timestamptz columns.
func fullCommand() command.Command {
	return command.Command{ID: "c-9", OrderID: "to-9", LegRef: "to-9.1.1", Kind: command.CancelLeg, Key: "cancel-to-9.1.1",
		Status: command.StatusPending, Attempts: 3, CreatedAt: t0, NextAttemptAt: t0, Deadline: t0.Add(5 * time.Minute), LastError: "boom"}
}

func fullAnswer() idempotency.IdempotencyRecord {
	return idempotency.IdempotencyRecord{Key: "k-9", Fingerprint: "f-9", Status: 202, ContentType: "application/json",
		Body: []byte(`{"transport_order_id":"to-9"}`), CreatedAt: t0}
}
