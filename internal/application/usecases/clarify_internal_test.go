package usecases

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/IQVO/material-flow-control/internal/adapters/outbound/memory"
	"github.com/IQVO/material-flow-control/internal/config"
	"github.com/IQVO/material-flow-control/internal/domain/containertrack"
	"github.com/IQVO/material-flow-control/internal/domain/transportorder"
	"github.com/IQVO/material-flow-control/internal/domain/transportrequest"
)

// ruling P4-R5, use-case side: the clarify path (whose fact handlers land in Task 10) run twice for one container
// publishes ContainerSentToClarification once and keeps the first hold. In-package because clarify is unexported.
func TestTheClarifyPathPublishesOneClarificationPerContainer(t *testing.T) {
	ctx := context.Background()
	t0 := time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)
	l, err := config.LoadLayout("../../../config/flow-layout.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	st, clock := memory.NewStore(), memory.NewClock(t0)
	events, tracks := memory.NewEvents(st), memory.NewTracks(st)
	s, err := NewService(l, DefaultConfig(), Deps{UoW: memory.NewUnitOfWork(st), Lock: memory.FlowLock{}, Requests: memory.NewRequests(st),
		Orders: memory.NewOrders(st), Tracks: tracks, Stations: memory.NewStations(st), Capacities: memory.NewCapacities(st),
		Units: memory.NewUnits(st), Commands: memory.NewCommands(st), Inbox: memory.NewInbox(st), Sink: events, Clock: clock,
		IDs: memory.NewIDs("u"), Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	stored := containertrack.New("TOTE-1", t0)
	stored.MoveTo(containertrack.Position{Kind: containertrack.InAsrs, Code: "WH1-AUTO-ASRS-ASRS1-00-00-S"}, t0)
	if err := tracks.Save(ctx, stored); err != nil {
		t.Fatal(err)
	}
	v, err := s.RequestTransport(ctx, transportrequest.Request{ID: "rq-1", ContainerID: "TOTE-1", Source: "WH1-AUTO-ASRS-ASRS1-00-00-S",
		Destination: "ST1", Purpose: transportrequest.PurposePick, Priority: transportrequest.PriorityNormal, DueBy: t0.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	clarifyOnce(ctx, t, s, v.OrderID, containertrack.ReasonNoRead)
	clock.Advance(time.Hour)
	clarifyOnce(ctx, t, s, v.OrderID, containertrack.ReasonMismatch)
	var got []string
	for _, ev := range events.All() {
		got = append(got, fmt.Sprintf("%T", ev))
	}
	want := "[transportorder.TransportAccepted containertrack.ContainerSentToClarification transportorder.TransportFailed capacity.SubsystemCapacityChanged]"
	if fmt.Sprint(got) != want {
		t.Fatalf("events = %v, want %s", got, want)
	}
	tk, _ := tracks.Get(ctx, "TOTE-1")
	if hold, ok := tk.Clarification(); !ok || hold.Reason != containertrack.ReasonNoRead || !hold.Since.Equal(t0) {
		t.Fatalf("hold = %+v %v, want the first (NO_READ since t0)", hold, ok)
	}
}

func clarifyOnce(ctx context.Context, t *testing.T, s *Service, orderID string, reason containertrack.ClarificationReason) {
	t.Helper()
	port, ok := s.l.Resolve("WH1-AUTO-ASRS-ASRS1-01-00-OUT1")
	if !ok {
		t.Fatal("OUT1 is not a layout place")
	}
	err := s.write(ctx, func(ctx context.Context) error {
		f := s.newFlow(ctx)
		o, err := f.orderByID(orderID)
		if err != nil {
			return err
		}
		if err := f.clarify(o, containertrack.Clarification{Reason: reason}, port, transportorder.FailClarification, ""); err != nil {
			return err
		}
		return f.commit()
	})
	if err != nil {
		t.Fatal(err)
	}
}
