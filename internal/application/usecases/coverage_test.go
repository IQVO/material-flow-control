package usecases_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"testing"

	"github.com/IQVO/material-flow-control/internal/adapters/outbound/memory"
	"github.com/IQVO/material-flow-control/internal/application/ports"
	"github.com/IQVO/material-flow-control/internal/application/usecases"
	"github.com/IQVO/material-flow-control/internal/config"
	ct "github.com/IQVO/material-flow-control/internal/domain/containertrack"
	"github.com/IQVO/material-flow-control/internal/domain/stationqueue"
	"github.com/IQVO/material-flow-control/internal/domain/transportorder"
	tr "github.com/IQVO/material-flow-control/internal/domain/transportrequest"
)

// ruling P4-R8: these tests cover the Task 9 write and read side that no later brief reaches (the read views'
// branches, RejectedError.Error and NewService's refusals).

var errRead = errors.New("read failed")

type failingTracks struct{ ports.ContainerTracks }

func (failingTracks) Get(context.Context, string) (*ct.Track, error) { return nil, errRead }

type failingStations struct{ ports.StationQueues }

func (failingStations) Get(context.Context, string) (*stationqueue.Queue, error) { return nil, errRead }

type failingOrders struct{ ports.TransportOrders }

func (failingOrders) Get(context.Context, string) (*transportorder.Order, error) { return nil, errRead }

func coverageDeps(s *memory.Store) usecases.Deps {
	return usecases.Deps{
		UoW: memory.NewUnitOfWork(s), Lock: memory.FlowLock{}, Requests: memory.NewRequests(s), Orders: memory.NewOrders(s),
		Tracks: memory.NewTracks(s), Stations: memory.NewStations(s), Capacities: memory.NewCapacities(s), Units: memory.NewUnits(s),
		Commands: memory.NewCommands(s), Inbox: memory.NewInbox(s), Sink: memory.NewEvents(s), Clock: memory.NewClock(t0),
		IDs: memory.NewIDs("u"), Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func TestNewServiceRefusesAMissingLayoutOrDependency(t *testing.T) {
	l, err := config.LoadLayout("../../../config/flow-layout.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := usecases.NewService(nil, usecases.DefaultConfig(), coverageDeps(memory.NewStore())); err == nil ||
		err.Error() != "usecases: a layout is required" {
		t.Fatalf("nil layout = %v", err)
	}
	for _, name := range []string{"UoW", "Lock", "Requests", "Orders", "Tracks", "Stations", "Capacities", "Units", "Commands",
		"Inbox", "Sink", "Clock", "IDs"} {
		d := coverageDeps(memory.NewStore())
		f := reflect.ValueOf(&d).Elem().FieldByName(name)
		f.Set(reflect.Zero(f.Type()))
		if _, err := usecases.NewService(l, usecases.DefaultConfig(), d); err == nil || err.Error() != "usecases: dependency "+name+" is required" {
			t.Errorf("nil %s = %v", name, err)
		}
	}
	d := coverageDeps(memory.NewStore())
	d.Log = nil
	if s, err := usecases.NewService(l, usecases.DefaultConfig(), d); err != nil || s.Layout() != l {
		t.Fatalf("a nil logger defaults: %v", err)
	}
}

func TestOrderDetailFindsTheOrderAndRefusesUnknownAndRejectedRequests(t *testing.T) {
	e := newEnv(t)
	v := e.mustAccept("rq-1", "TOTE-1", "ST1", pack, tr.PurposeOutbound)
	if o, err := e.svc.OrderDetail(ctx, "rq-1"); err != nil || o.ID != v.OrderID || o.RequestID != "rq-1" || len(o.Legs) != 1 {
		t.Fatalf("detail = %+v %v", o, err)
	}
	if _, err := e.svc.OrderDetail(ctx, "rq-none"); !errors.Is(err, usecases.ErrRequestNotFound) {
		t.Fatalf("unknown request = %v", err)
	}
	_, err := e.request("rq-2", "TOTE-2", decant, "WH1-AUTO-ASRS-ASRS2-00-00-S", tr.PurposePutaway)
	var r *usecases.RejectedError
	if !errors.As(err, &r) || err.Error() != "transport request rq-2 rejected: DESTINATION_UNREACHABLE (the source or the destination is not a place of this automation)" {
		t.Fatalf("rejection = %v", err)
	}
	if _, err := e.svc.OrderDetail(ctx, "rq-2"); !errors.Is(err, usecases.ErrTransportEnded) {
		t.Fatalf("rejected request detail = %v", err)
	}
}

func TestTrackAndStationViewsAnswerNotFound(t *testing.T) {
	e := newEnv(t)
	if _, err := e.svc.Track(ctx, "TOTE-NONE"); !errors.Is(err, usecases.ErrTrackNotFound) {
		t.Fatalf("unknown track = %v", err)
	}
	e.stored("TOTE-1")
	if v, err := e.svc.Track(ctx, "TOTE-1"); err != nil || v.Track.ContainerID != "TOTE-1" {
		t.Fatalf("track = %+v %v", v, err)
	}
	if _, err := e.svc.Station(ctx, "ST-NONE"); !errors.Is(err, usecases.ErrStationNotFound) {
		t.Fatalf("unknown station = %v", err)
	}
	if q, err := e.svc.Station(ctx, "ST1"); err != nil || q.StationID != "ST1" || q.BufferLimit != 2 || len(q.Occupants) != 0 {
		t.Fatalf("an empty station = %+v %v", q, err)
	}
	if u, err := e.svc.EquipmentUnits(ctx, "ASRS1"); err != nil || len(u) != 0 {
		t.Fatalf("no unit reported yet = %+v %v", u, err)
	}
}

func TestReadViewsPassARepositoryErrorThrough(t *testing.T) {
	l, err := config.LoadLayout("../../../config/flow-layout.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	s := memory.NewStore()
	d := coverageDeps(s)
	d.Tracks, d.Stations, d.Orders = failingTracks{d.Tracks}, failingStations{d.Stations}, failingOrders{d.Orders}
	svc, err := usecases.NewService(l, usecases.DefaultConfig(), d)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Track(ctx, "TOTE-1"); !errors.Is(err, errRead) {
		t.Fatalf("track = %v", err)
	}
	if _, err := svc.Station(ctx, "ST1"); !errors.Is(err, errRead) {
		t.Fatalf("station = %v", err)
	}
	if err := memory.NewRequests(s).Insert(ctx, tr.Record{Request: tr.Request{ID: "rq-1"}, OrderID: "to-1",
		Outcome: tr.OutcomeAccepted}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.OrderDetail(ctx, "rq-1"); !errors.Is(err, errRead) {
		t.Fatalf("order detail = %v", err)
	}
}
