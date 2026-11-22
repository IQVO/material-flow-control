//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

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

var t0 = time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)

func request() tr.Request {
	return tr.Request{ID: "rq-1", ContainerID: "TOTE-1", Source: "WH1-RCV-DEC1", Destination: "WH1-PCK-PK1", Purpose: tr.PurposeOutbound,
		Priority: tr.PriorityUrgent, DueBy: t0.Add(time.Hour)}
}

// The two round-trip tests below are split into t.Helper steps (one per
// repository) so each function fits cyclop's limit under the integration tag;
// every assertion is byte-identical to the single-function version (Task 26).
func TestRequestsOrdersAndTracksRoundTrip(t *testing.T) {
	pool := pgtest.Pool(t)
	ctx := context.Background()
	reqs, orders, tracks := postgres.NewRequests(pool), postgres.NewOrders(pool), postgres.NewTracks(pool)

	requestsRoundTrip(ctx, t, reqs)
	src := layout.Place{Kind: layout.PlaceHandoff, Code: "WH1-RCV-DEC1", HandoffPointID: "ST2", HandoffKind: layout.HandoffDecant}
	dst := layout.Place{Kind: layout.PlaceHandoff, Code: "WH1-PCK-PK1", HandoffPointID: "PK1", HandoffKind: layout.HandoffPack}
	ordersRoundTrip(ctx, t, orders, src, dst)
	tracksRoundTrip(ctx, t, tracks, src)
}

func requestsRoundTrip(ctx context.Context, t *testing.T, reqs *postgres.Requests) {
	t.Helper()
	rec := tr.Accept(request(), "to-1", t0)
	if err := reqs.Insert(ctx, rec); err != nil {
		t.Fatal(err)
	}
	if err := reqs.Insert(ctx, rec); !errors.Is(err, ports.ErrDuplicate) {
		t.Fatalf("second insert = %v", err)
	}
	got, err := reqs.Get(ctx, "rq-1")
	if err != nil || !got.SameRequest(request()) || got.OrderID != "to-1" || !got.At.Equal(t0) {
		t.Fatalf("record = %+v %v", got, err)
	}
	if _, err := reqs.Get(ctx, "nope"); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("unknown = %v", err)
	}
}

func ordersRoundTrip(ctx context.Context, t *testing.T, orders *postgres.Orders, src, dst layout.Place) {
	t.Helper()
	o, _ := to.Accept("to-1", request(), src, dst, []routing.LegPlan{{Subsystem: routing.SubsystemAmr, SubsystemID: "AMR1", Kind: routing.KindMove, From: src, To: dst}}, t0)
	o.Next(t0)
	if err := orders.Insert(ctx, o); err != nil {
		t.Fatal(err)
	}
	byRef, err := orders.ByLegRef(ctx, "to-1.1.1")
	if err != nil || byRef.ID() != "to-1" || byRef.Legs()[0].Status != to.LegPending || byRef.Legs()[0].From != src {
		t.Fatalf("ByLegRef = %+v %v", byRef, err)
	}
	_ = byRef.LegFailed("to-1.1.1", t0)
	retry, _ := byRef.RetryLeg("to-1.1.1", t0)
	if err := orders.Save(ctx, byRef); err != nil {
		t.Fatal(err)
	}
	if again, err := orders.ByLegRef(ctx, retry.Ref); err != nil || again.Retries() != 1 || again.Version() != 2 {
		t.Fatalf("retry ref = %+v %v", again, err)
	}
	open, _ := orders.Open(ctx)
	if len(open) != 1 {
		t.Fatalf("open = %d", len(open))
	}
}

func tracksRoundTrip(ctx context.Context, t *testing.T, tracks *postgres.Tracks, src layout.Place) {
	t.Helper()
	track := ct.New("TOTE-1", t0)
	_ = track.Reserve("to-1", "rq-1", t0)
	track.MoveTo(ct.At(src), t0)
	track.MoveTo(ct.OnRobotOf("AMR1", "EMU/R1"), t0.Add(time.Second))
	if err := tracks.Save(ctx, track); err != nil {
		t.Fatal(err)
	}
	loaded, err := tracks.Get(ctx, "TOTE-1")
	if err != nil || loaded.Position() != ct.OnRobotOf("AMR1", "EMU/R1") || loaded.ActiveRequestID() != "rq-1" || loaded.Version() != 1 {
		t.Fatalf("track = %+v %v", loaded, err)
	}
	h, _ := tracks.History(ctx, "TOTE-1", 1)
	if len(h) != 1 || h[0].To.Kind != ct.OnRobot || h[0].RequestID != "rq-1" {
		t.Fatalf("history = %+v", h)
	}
}

func TestQueuesCapacitiesUnitsCommandsAndTheInbox(t *testing.T) {
	pool := pgtest.Pool(t)
	ctx := context.Background()
	stationsRoundTrip(ctx, t, postgres.NewStations(pool))
	capacitiesRoundTrip(ctx, t, postgres.NewCapacities(pool))
	unitsRoundTrip(ctx, t, postgres.NewUnits(pool))
	commandsRoundTrip(ctx, t, postgres.NewCommands(pool))
	inboxAndIdempotencyRoundTrip(ctx, t, postgres.NewProcessedEvents(pool), postgres.NewIdempotencyStore(pool))
}

func stationsRoundTrip(ctx context.Context, t *testing.T, stations *postgres.Stations) {
	t.Helper()
	q := sq.New("ST1", 2)
	q.Join(sq.Entry{OrderID: "to-1", ContainerID: "TOTE-1", Priority: tr.PriorityNormal, DueBy: t0, ReadyAt: t0})
	q.Admit(t0)
	if err := stations.Save(ctx, q); err != nil {
		t.Fatal(err)
	}
	if got, err := stations.Get(ctx, "ST1"); err != nil || got.Occupied() != 1 || got.Position("TOTE-1") != 1 {
		t.Fatalf("queue = %+v %v", got, err)
	}
}

func capacitiesRoundTrip(ctx context.Context, t *testing.T, caps *postgres.Capacities) {
	t.Helper()
	_ = caps.Save(ctx, capacity.Asrs("ASRS1", 2, 1, 3, 60, t0))
	_ = caps.Save(ctx, capacity.Fleet("AMR1", 3, 3, 2, 0, 1, 30, t0))
	if snap, found, err := caps.Get(ctx, capacity.KindAsrs, "ASRS1"); err != nil || !found || snap != capacity.Asrs("ASRS1", 2, 1, 3, 60, t0) {
		t.Fatalf("capacity = %+v %v %v", snap, found, err)
	}
	if list, _ := caps.List(ctx); len(list) != 2 || list[0].Kind != capacity.KindAmrFleet {
		t.Fatalf("list = %+v", list)
	}
}

func unitsRoundTrip(ctx context.Context, t *testing.T, units *postgres.Units) {
	t.Helper()
	_ = units.Save(ctx, facts.EquipmentState{AsrsID: "ASRS1", UnitID: "A1", Aisle: 1, State: "HELD", At: t0})
	if u, _ := units.List(ctx, "ASRS1"); len(u) != 1 || u[0].State != "HELD" {
		t.Fatalf("units = %+v", u)
	}
}

func commandsRoundTrip(ctx context.Context, t *testing.T, cmds *postgres.Commands) {
	t.Helper()
	a := command.New("c-a", "to-1", "to-1.1.1", command.RequestLeg, t0, time.Minute)
	b := command.New("c-b", "to-2", "to-2.1.1", command.ConfirmPickup, t0, time.Minute)
	_ = cmds.Enqueue(ctx, a)
	_ = cmds.Enqueue(ctx, b)
	due, err := cmds.Due(ctx, t0, 10)
	if err != nil || len(due) != 2 || due[0].ID != "c-a" || due[0].Seq >= due[1].Seq || due[1].Key != "pickup-to-2.1.1" {
		t.Fatalf("due = %+v %v", due, err)
	}
	_ = cmds.Save(ctx, due[0].Retry(t0, time.Second, time.Minute, "boom"))
	if due, _ := cmds.Due(ctx, t0, 10); len(due) != 1 || due[0].ID != "c-b" {
		t.Fatalf("after a retry = %+v", due)
	}
}

func inboxAndIdempotencyRoundTrip(ctx context.Context, t *testing.T, inbox *postgres.ProcessedEvents, idem *postgres.IdempotencyStore) {
	t.Helper()
	first, _ := inbox.Claim(ctx, "e1", "T", t0)
	again, _ := inbox.Claim(ctx, "e1", "T", t0)
	if !first || again {
		t.Fatalf("claims = %v %v", first, again)
	}

	rec := idempotency.IdempotencyRecord{Key: "k", Fingerprint: "f", Status: 422, ContentType: "application/problem+json", Body: []byte("{}"), CreatedAt: t0}
	_ = idem.Save(ctx, rec, t0.Add(-24*time.Hour))
	if got, found, _ := idem.Lookup(ctx, "k", t0.Add(-time.Hour)); !found || got.Status != 422 {
		t.Fatalf("idempotency = %+v %v", got, found)
	}
}

func TestARolledBackUnitOfWorkLeavesNoRowAndNoClaim(t *testing.T) {
	pool := pgtest.Pool(t)
	ctx := context.Background()
	uow, inbox, tracks := postgres.NewUnitOfWork(pool), postgres.NewProcessedEvents(pool), postgres.NewTracks(pool)
	boom := errors.New("boom")
	err := uow.Do(ctx, func(ctx context.Context) error {
		if _, err := inbox.Claim(ctx, "e1", "T", t0); err != nil {
			return err
		}
		if err := tracks.Save(ctx, ct.New("TOTE-1", t0)); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatal(err)
	}
	if fresh, _ := inbox.Claim(ctx, "e1", "T", t0); !fresh {
		t.Fatal("the claim survived the rollback")
	}
	if _, err := tracks.Get(ctx, "TOTE-1"); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("the track survived the rollback: %v", err)
	}
}
