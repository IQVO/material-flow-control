package usecases_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/IQVO/material-flow-control/internal/adapters/outbound/memory"
	"github.com/IQVO/material-flow-control/internal/application/command"
	"github.com/IQVO/material-flow-control/internal/application/usecases"
	"github.com/IQVO/material-flow-control/internal/config"
	ct "github.com/IQVO/material-flow-control/internal/domain/containertrack"
	tr "github.com/IQVO/material-flow-control/internal/domain/transportrequest"
)

var (
	t0  = time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)
	ctx = context.Background()
)

const (
	asrsLoc = "WH1-AUTO-ASRS-ASRS1-00-00-S"
	out1Loc = "WH1-AUTO-ASRS-ASRS1-01-00-OUT1"
	in1Loc  = "WH1-AUTO-ASRS-ASRS1-01-00-IN1"
	decant  = "WH1-RCV-DEC1"
	pack    = "WH1-PCK-PK1"
	clrLoc  = "WH1-CLR-CLR1"
)

type env struct {
	t       *testing.T
	svc     *usecases.Service
	store   *memory.Store
	clock   *memory.Clock
	events  *memory.Events
	cmds    *memory.Commands
	tracks  *memory.Tracks
	caps    *memory.Capacities
	orders  *memory.Orders
	asrs    *memory.Asrs
	amr     *memory.Amr
	metrics *memory.Metrics
	eventN  int //nolint:unused // Task 9 report D2: used by Task 10's fact tests (e.id())
}

func newEnv(t *testing.T) *env {
	t.Helper()
	l, err := config.LoadLayout("../../../config/flow-layout.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	s := memory.NewStore()
	e := &env{t: t, store: s, clock: memory.NewClock(t0), events: memory.NewEvents(s), cmds: memory.NewCommands(s),
		tracks: memory.NewTracks(s), caps: memory.NewCapacities(s), orders: memory.NewOrders(s), asrs: memory.NewAsrs(),
		amr: memory.NewAmr(), metrics: &memory.Metrics{}}
	e.svc, err = usecases.NewService(l, usecases.DefaultConfig(), usecases.Deps{
		UoW: memory.NewUnitOfWork(s), Lock: memory.FlowLock{}, Requests: memory.NewRequests(s), Orders: e.orders, Tracks: e.tracks,
		Stations: memory.NewStations(s), Capacities: e.caps, Units: memory.NewUnits(s), Commands: e.cmds, Inbox: memory.NewInbox(s),
		Sink: e.events, Asrs: e.asrs, Amr: e.amr, Clock: e.clock, IDs: memory.NewIDs("u"), Metrics: e.metrics, Kick: &memory.Kicker{},
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// stored puts a container in the AS/RS, as a ContainerStored fact would.
func (e *env) stored(container string) {
	e.t.Helper()
	track := ct.New(container, t0)
	track.MoveTo(ct.Position{Kind: ct.InAsrs, Code: asrsLoc}, t0)
	if err := e.tracks.Save(ctx, track); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) request(id, container, src, dst string, p tr.Purpose) (usecases.TransportView, error) {
	return e.svc.RequestTransport(ctx, tr.Request{ID: id, ContainerID: container, Source: src, Destination: dst, Purpose: p,
		Priority: tr.PriorityNormal, DueBy: t0.Add(time.Hour)})
}

func (e *env) mustAccept(id, container, src, dst string, p tr.Purpose) usecases.TransportView {
	e.t.Helper()
	v, err := e.request(id, container, src, dst, p)
	if err != nil {
		e.t.Fatalf("%s: %v", id, err)
	}
	return v
}

// types lists the committed event types as "<package>.<Name>".
func (e *env) types() []string {
	var out []string
	for _, ev := range e.events.All() {
		out = append(out, fmt.Sprintf("%T", ev))
	}
	return out
}

func (e *env) pending(kind command.Kind) []command.Command {
	var out []command.Command
	for _, c := range e.cmds.All() {
		if c.Kind == kind && c.Status == command.StatusPending {
			out = append(out, c)
		}
	}
	return out
}

//nolint:unused // Task 9 report D2: used by Task 10's fact tests
func (e *env) id() string {
	e.eventN++
	return fmt.Sprintf("ev-%d", e.eventN)
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
