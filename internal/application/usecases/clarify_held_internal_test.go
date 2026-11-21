package usecases

import (
	"context"
	"io"
	"log/slog"
	"strconv"
	"testing"
	"time"

	"github.com/IQVO/material-flow-control/internal/adapters/outbound/memory"
	"github.com/IQVO/material-flow-control/internal/config"
	"github.com/IQVO/material-flow-control/internal/domain/containertrack"
	"github.com/IQVO/material-flow-control/internal/domain/transportrequest"
)

// carry (Task 9 review note): clarify on an already-held container at an AS/RS output port builds no second
// internal order, so it consumes no id (before, it drew an IDs.NewID that ReserveInternal then refused).
func TestClarifyOnAHeldContainerBuildsNoSecondInternalOrder(t *testing.T) {
	ctx := context.Background()
	t0 := time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)
	l, err := config.LoadLayout("../../../config/flow-layout.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	st, ids := memory.NewStore(), memory.NewIDs("u")
	tracks, orders := memory.NewTracks(st), memory.NewOrders(st)
	s, err := NewService(l, DefaultConfig(), Deps{UoW: memory.NewUnitOfWork(st), Lock: memory.FlowLock{}, Requests: memory.NewRequests(st),
		Orders: orders, Tracks: tracks, Stations: memory.NewStations(st), Capacities: memory.NewCapacities(st),
		Units: memory.NewUnits(st), Commands: memory.NewCommands(st), Inbox: memory.NewInbox(st), Sink: memory.NewEvents(st),
		Clock: memory.NewClock(t0), IDs: ids, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	port, _ := l.Resolve("WH1-AUTO-ASRS-ASRS1-01-00-OUT1")
	onPort := containertrack.New("TOTE-1", t0)
	onPort.MoveTo(containertrack.At(port), t0)
	if err := tracks.Save(ctx, onPort); err != nil {
		t.Fatal(err)
	}
	v, err := s.RequestTransport(ctx, transportrequest.Request{ID: "rq-1", ContainerID: "TOTE-1", Source: port.Code,
		Destination: "ST1", Purpose: transportrequest.PurposeOutbound, Priority: transportrequest.PriorityNormal, DueBy: t0.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	clarifyOnce(ctx, t, s, v.OrderID, containertrack.ReasonNoRead)
	tk, _ := tracks.Get(ctx, "TOTE-1")
	internal := tk.ActiveOrderID()
	if _, held := tk.Clarification(); !held || internal == "" || internal == v.OrderID {
		t.Fatalf("the first clarify holds the container and moves it by an internal order: %+v", tk.Snapshot())
	}
	before, _ := strconv.Atoi(ids.NewID()[len("u-"):])
	clarifyOnce(ctx, t, s, v.OrderID, containertrack.ReasonMismatch)
	if after := ids.NewID(); after != "u-"+strconv.Itoa(before+1) {
		t.Fatalf("the second clarify drew an id: u-%d then %s", before, after)
	}
	if tk, _ := tracks.Get(ctx, "TOTE-1"); tk.ActiveOrderID() != internal {
		t.Fatalf("the internal order changed: %s, want %s", tk.ActiveOrderID(), internal)
	}
}
