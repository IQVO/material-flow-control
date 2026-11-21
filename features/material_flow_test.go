package features_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/IQVO/material-flow-control/internal/adapters/outbound/memory"
	"github.com/IQVO/material-flow-control/internal/application/facts"
	"github.com/IQVO/material-flow-control/internal/application/usecases"
	"github.com/IQVO/material-flow-control/internal/config"
	"github.com/IQVO/material-flow-control/internal/domain/capacity"
	ct "github.com/IQVO/material-flow-control/internal/domain/containertrack"
	"github.com/IQVO/material-flow-control/internal/domain/transportorder"
	tr "github.com/IQVO/material-flow-control/internal/domain/transportrequest"
)

var t0 = time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)

type world struct {
	svc     *usecases.Service
	events  *memory.Events
	orders  *memory.Orders
	n       int
	answers []usecases.TransportView
	errs    map[string]error
}

func (w *world) id() string { w.n++; return fmt.Sprintf("ev-%d", w.n) }

func (w *world) layout() error {
	l, err := config.LoadLayout("../config/flow-layout.example.yaml")
	if err != nil {
		return err
	}
	s := memory.NewStore()
	w.events, w.orders, w.errs = memory.NewEvents(s), memory.NewOrders(s), map[string]error{}
	w.svc, err = usecases.NewService(l, usecases.DefaultConfig(), usecases.Deps{UoW: memory.NewUnitOfWork(s), Lock: memory.FlowLock{},
		Requests: memory.NewRequests(s), Orders: w.orders, Tracks: memory.NewTracks(s), Stations: memory.NewStations(s),
		Capacities: memory.NewCapacities(s), Units: memory.NewUnits(s), Commands: memory.NewCommands(s), Inbox: memory.NewInbox(s),
		Sink: w.events, Asrs: memory.NewAsrs(), Amr: memory.NewAmr(), Clock: memory.NewClock(t0), IDs: memory.NewIDs("b"),
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	return err
}

func (w *world) stored(container string) error {
	return w.svc.OnContainerStored(context.Background(), w.id(), "ContainerStored", facts.ContainerStored{AsrsID: "ASRS1", ContainerID: container, PortID: "IN1"})
}

func (w *world) request(container, src, dst, purpose, id string) error {
	v, err := w.svc.RequestTransport(context.Background(), tr.Request{ID: id, ContainerID: container, Source: src, Destination: dst,
		Purpose: tr.Purpose(purpose), Priority: tr.PriorityNormal, DueBy: t0.Add(time.Hour)})
	w.errs[id] = err
	if err == nil {
		w.answers = append(w.answers, v)
	}
	return nil
}

// current is the leg in flight of the container's active transport.
func (w *world) current(container string) (transportorder.Leg, error) {
	tv, err := w.svc.Track(context.Background(), container)
	if err != nil {
		return transportorder.Leg{}, err
	}
	open, _ := w.orders.Open(context.Background())
	for _, o := range open {
		if o.ID() == tv.Track.ActiveOrderID {
			if leg, ok := o.Current(); ok {
				return leg, nil
			}
		}
	}
	return transportorder.Leg{}, fmt.Errorf("%s has no leg in flight", container)
}

func (w *world) presented(container, port, scan string) error {
	leg, err := w.current(container)
	if err != nil {
		return err
	}
	return w.svc.OnContainerPresented(context.Background(), w.id(), "ContainerPresentedAtPort", facts.ContainerPresented{AsrsID: "ASRS1",
		ContainerID: container, PortID: port, Reference: leg.Ref, ScanResult: scan})
}

func (w *world) carried(container, to string) error {
	leg, err := w.current(container)
	if err != nil {
		return err
	}
	ctx := context.Background()
	if err := w.svc.OnContainerPickedUp(ctx, w.id(), "ContainerPickedUp", facts.ContainerPickedUp{FleetID: "AMR1", ContainerID: container,
		From: leg.From.HandoffPointID, Reference: leg.Ref, Robot: "EMU/R1"}); err != nil {
		return err
	}
	return w.svc.OnLegCompleted(ctx, w.id(), "LegCompleted", facts.LegCompleted{FleetID: "AMR1", ContainerID: container, To: to, Reference: leg.Ref, Robot: "EMU/R1"})
}

func (w *world) storedLeg(container string) error {
	leg, err := w.current(container)
	if err != nil {
		return err
	}
	return w.svc.OnContainerStored(context.Background(), w.id(), "ContainerStored", facts.ContainerStored{AsrsID: "ASRS1", ContainerID: container,
		PortID: leg.From.PortID, Reference: leg.Ref})
}

func name(ev any) string {
	s := fmt.Sprintf("%T", ev)
	return s[strings.LastIndex(s, ".")+1:]
}

func (w *world) arrived(station string, pos int) error {
	for _, ev := range w.events.All() {
		if a, ok := ev.(transportorder.ContainerArrivedAtStation); ok && a.StationID == station && a.QueuePosition == pos {
			return nil
		}
	}
	return fmt.Errorf("no arrival at %s position %d", station, pos)
}

func (w *world) status(id, want string) error {
	v, err := w.svc.Transport(context.Background(), id)
	if err != nil || v.Status != want {
		return fmt.Errorf("transport %s = %+v %v, want %s", id, v, err, want)
	}
	return nil
}

func (w *world) failed(id, reason string) error {
	v, _ := w.svc.Transport(context.Background(), id)
	if v.Status != usecases.ViewFailed || v.Reason != reason {
		return fmt.Errorf("transport %s = %+v", id, v)
	}
	return nil
}

func (w *world) eventsOf(id, list string) error {
	var got []string
	for _, ev := range w.events.All() {
		if strings.Contains(fmt.Sprintf("%+v", ev), "RequestID:"+id+" ") {
			got = append(got, name(ev))
		}
	}
	if strings.Join(got, ",") != list {
		return fmt.Errorf("events of %s = %v, want %s", id, got, list)
	}
	return nil
}

func (w *world) at(container, kind, code string) error {
	tv, err := w.svc.Track(context.Background(), container)
	if err != nil || tv.Track.Position.Kind != ct.PositionKind(kind) || tv.Track.Position.Code != code {
		return fmt.Errorf("%s = %+v %v", container, tv.Track.Position, err)
	}
	return nil
}

func (w *world) noVocabulary() error {
	for _, ev := range w.events.All() {
		s := fmt.Sprintf("%+v", ev)
		for _, word := range []string{"EMU/R1", "Robot:", "Node", "Aisle", "Slot", "sj-", "leg-"} {
			if strings.Contains(s, word) {
				return fmt.Errorf("%s carries %q: %s", name(ev), word, s)
			}
		}
	}
	return nil
}

func (w *world) rejected(id, reason string) error {
	var r *usecases.RejectedError
	if err := w.errs[id]; err == nil || !strings.Contains(err.Error(), reason) || !asRejected(err, &r) {
		return fmt.Errorf("request %s = %v", id, err)
	}
	return nil
}

func asRejected(err error, r **usecases.RejectedError) bool {
	re, ok := err.(*usecases.RejectedError)
	*r = re
	return ok
}

func (w *world) ordersFor(n int, container string) error {
	open, _ := w.orders.Open(context.Background())
	got := 0
	for _, o := range open {
		if o.ContainerID() == container {
			got++
		}
	}
	if got != n {
		return fmt.Errorf("%d orders for %s", got, container)
	}
	return nil
}

func (w *world) sameOrder() error {
	if len(w.answers) != 2 || w.answers[0].OrderID != w.answers[1].OrderID {
		return fmt.Errorf("answers = %+v", w.answers)
	}
	return nil
}

func (w *world) fleet(total, online, available int) error {
	return w.svc.OnFleetCapacityChanged(context.Background(), w.id(), "FleetCapacityChanged",
		facts.FleetCapacity{FleetID: "AMR1", RobotsTotal: total, RobotsOnline: online, RobotsAvailable: available})
}

func (w *world) lastCapacity(kind, status string, available int) error {
	all := w.events.All()
	for i := len(all) - 1; i >= 0; i-- {
		if c, ok := all[i].(capacity.SubsystemCapacityChanged); ok && string(c.Kind) == kind {
			if string(c.Status) != status || c.UnitsAvailable != available {
				return fmt.Errorf("last %s = %+v", kind, c.Snapshot)
			}
			return nil
		}
	}
	return fmt.Errorf("no %s capacity event", kind)
}

func scenario(sc *godog.ScenarioContext) {
	w := &world{}
	sc.Step(`^the example material-flow layout$`, w.layout)
	sc.Step(`^container "([^"]*)" is stored in the AS/RS$`, w.stored)
	sc.Step(`^the WES requests "([^"]*)" from "([^"]*)" to "([^"]*)" for "([^"]*)" as "([^"]*)"$`, w.request)
	sc.Step(`^the AS/RS presents "([^"]*)" at port "([^"]*)" with scan "([^"]*)"$`, w.presented)
	sc.Step(`^a robot carries "([^"]*)" to "([^"]*)"$`, w.carried)
	sc.Step(`^the AS/RS stores "([^"]*)"$`, w.storedLeg)
	sc.Step(`^"ContainerArrivedAtStation" is published for station "([^"]*)" at queue position (\d+)$`, w.arrived)
	sc.Step(`^transport "([^"]*)" is "([^"]*)"$`, w.status)
	sc.Step(`^transport "([^"]*)" failed "([^"]*)"$`, w.failed)
	sc.Step(`^the events of "([^"]*)" are "([^"]*)"$`, w.eventsOf)
	sc.Step(`^container "([^"]*)" is "([^"]*)" at "([^"]*)"$`, w.at)
	sc.Step(`^no event names a robot, a node, a rack position or a leg$`, w.noVocabulary)
	sc.Step(`^request "([^"]*)" is rejected "([^"]*)"$`, w.rejected)
	sc.Step(`^(\d+) transport order exists for "([^"]*)"$`, w.ordersFor)
	sc.Step(`^both answers name the same transport order$`, w.sameOrder)
	sc.Step(`^the AMR fleet reports (\d+) robots, (\d+) online, (\d+) available$`, w.fleet)
	sc.Step(`^the last "([^"]*)" capacity is "([^"]*)" with (\d+) available$`, w.lastCapacity)
}

func TestFeatures(t *testing.T) {
	suite := godog.TestSuite{ScenarioInitializer: scenario,
		Options: &godog.Options{Format: "pretty", Paths: []string{"material_flow.feature"}, Strict: true, TestingT: t}}
	if suite.Run() != 0 {
		t.Fatal("feature scenarios failed")
	}
}
