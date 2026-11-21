package usecases_test

import (
	"reflect"
	"testing"

	"github.com/IQVO/material-flow-control/internal/adapters/outbound/memory"
	"github.com/IQVO/material-flow-control/internal/application/usecases"
	"github.com/IQVO/material-flow-control/internal/config"
	tr "github.com/IQVO/material-flow-control/internal/domain/transportrequest"
)

func TestTheReaderSeesWhatTheServiceWroteAndHasNoWriteMethod(t *testing.T) {
	e := newEnv(t)
	e.stored("TOTE-1")
	v := e.mustAccept("rq-1", "TOTE-1", asrsLoc, "ST1", tr.PurposePick)
	l, _ := config.LoadLayout("../../../config/flow-layout.example.yaml")
	r, err := usecases.NewReader(l, usecases.DefaultConfig(), usecases.ReadDeps{Requests: memory.NewRequests(e.store), Orders: e.orders,
		Tracks: e.tracks, Stations: memory.NewStations(e.store), Capacities: e.caps, Units: memory.NewUnits(e.store), Clock: e.clock})
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.Transport(ctx, "rq-1")
	if err != nil || got.OrderID != v.OrderID || got.Status != usecases.ViewInProgress {
		t.Fatalf("reader view = %+v %v", got, err)
	}
	if d, err := r.OrderDetail(ctx, "rq-1"); err != nil || len(d.Legs) != 2 {
		t.Fatalf("detail = %+v %v", d, err)
	}
	want := map[string]bool{"Transport": true, "OrderDetail": true, "Track": true, "Station": true, "Subsystems": true,
		"EquipmentUnits": true, "Layout": true}
	rt := reflect.TypeOf(r)
	if rt.NumMethod() != len(want) {
		t.Fatalf("Reader has %d methods, want exactly the %d reads", rt.NumMethod(), len(want))
	}
	for i := 0; i < rt.NumMethod(); i++ {
		if !want[rt.Method(i).Name] {
			t.Fatalf("Reader exposes %s", rt.Method(i).Name)
		}
	}
	if _, err := usecases.NewReader(l, usecases.DefaultConfig(), usecases.ReadDeps{}); err == nil {
		t.Fatal("a reader without repositories was built")
	}
}
