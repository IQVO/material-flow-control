//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/IQVO/material-flow-control/internal/application/command"
	"github.com/IQVO/material-flow-control/internal/application/facts"
	"github.com/IQVO/material-flow-control/internal/application/ports"
	"github.com/IQVO/material-flow-control/internal/domain/capacity"
	ct "github.com/IQVO/material-flow-control/internal/domain/containertrack"
	sq "github.com/IQVO/material-flow-control/internal/domain/stationqueue"
	to "github.com/IQVO/material-flow-control/internal/domain/transportorder"
)

var bg = context.Background()

func orderRoundTrip(t *testing.T, r repos) any {
	ok(t, r.orders.Insert(bg, to.Restore(fullOrder("to-9"))))
	got := must(r.orders.Get(bg, "to-9"))(t)
	byRef := must(r.orders.ByLegRef(bg, "to-9.1.2"))(t)
	open := must(r.orders.Open(bg))(t)
	ok(t, r.orders.Save(bg, got))
	saved := must(r.orders.Get(bg, "to-9"))(t)
	return []to.Snapshot{got.Snapshot(), byRef.Snapshot(), open[0].Snapshot(), saved.Snapshot()}
}

func TestAnOrderKeepsEverySnapshotField(t *testing.T) {
	in := fullOrder("to-9")
	nonZero(t, "transportorder.Snapshot", reflect.ValueOf(in))
	inserted, saved := in, in
	inserted.Version, saved.Version = in.Version+1, in.Version+2
	oracle(t, "order round trip", []to.Snapshot{inserted, inserted, inserted, saved}, orderRoundTrip)
}

type trackAnswer struct {
	Snapshot ct.Snapshot
	History  []ct.Move
}

var onRobot = ct.Position{Kind: ct.OnRobot, Code: "WH1-AUTO-ASRS-ASRS1-01-00-OUT1", Robot: "EMU/R2"}

func trackRoundTrip(t *testing.T, r repos) any {
	track := ct.Restore(fullTrack())
	track.MoveTo(onRobot, tn.Add(time.Second))
	ok(t, r.tracks.Save(bg, track))
	return trackAnswer{Snapshot: must(r.tracks.Get(bg, "TOTE-9"))(t).Snapshot(), History: must(r.tracks.History(bg, "TOTE-9", 10))(t)}
}

func TestATrackKeepsEverySnapshotFieldAndItsMoves(t *testing.T) {
	in := fullTrack()
	nonZero(t, "containertrack.Snapshot", reflect.ValueOf(in))
	want := trackAnswer{Snapshot: in, History: []ct.Move{{From: in.Position, To: onRobot, RequestID: "rq-9", At: tn.Add(time.Second)}}}
	want.Snapshot.Position, want.Snapshot.UpdatedAt, want.Snapshot.Version = onRobot, tn.Add(time.Second), in.Version+1
	want.Snapshot.Clarification = &ct.Clarification{}
	*want.Snapshot.Clarification = *in.Clarification
	nonZero(t, "containertrack.Move", reflect.ValueOf(want.History))
	oracle(t, "track round trip", want, trackRoundTrip)
}

type valueAnswer struct {
	Queue    sq.Snapshot
	Capacity capacity.Snapshot
	Found    bool
	List     []capacity.Snapshot
	Units    []facts.EquipmentState
	Record   any
}

func valuesRoundTrip(t *testing.T, r repos) any {
	ok(t, r.stations.Save(bg, sq.Restore(fullQueue())))
	ok(t, r.caps.Save(bg, fullCapacity()))
	ok(t, r.units.Save(bg, fullUnit()))
	ok(t, r.requests.Insert(bg, fullRecord()))
	c, found := must2(t)(r.caps.Get(bg, capacity.KindStation, "ST9"))
	return valueAnswer{Queue: must(r.stations.Get(bg, "ST9"))(t).Snapshot(), Capacity: c, Found: found,
		List: must(r.caps.List(bg))(t), Units: must(r.units.List(bg, "ASRS1"))(t), Record: must(r.requests.Get(bg, "rq-9"))(t)}
}

func must2(t *testing.T) func(capacity.Snapshot, bool, error) (capacity.Snapshot, bool) {
	return func(s capacity.Snapshot, found bool, err error) (capacity.Snapshot, bool) {
		t.Helper()
		ok(t, err)
		return s, found
	}
}

func TestQueuesCapacitiesUnitsAndRecordsKeepEveryField(t *testing.T) {
	for name, v := range map[string]any{"stationqueue.Snapshot": fullQueue(), "capacity.Snapshot": fullCapacity(),
		"facts.EquipmentState": fullUnit(), "transportrequest.Record": fullRecord()} {
		nonZero(t, name, reflect.ValueOf(v))
	}
	queue := fullQueue()
	queue.Version++
	want := valueAnswer{Queue: queue, Capacity: fullCapacity(), Found: true, List: []capacity.Snapshot{fullCapacity()},
		Units: []facts.EquipmentState{fullUnit()}, Record: fullRecord()}
	oracle(t, "value round trip", want, valuesRoundTrip)
}

var retried = func(c command.Command) command.Command {
	return c.Retry(t0.Add(time.Second), time.Second, time.Minute, "again")
}

func commandRoundTrip(t *testing.T, r repos) any {
	ok(t, r.commands.Enqueue(bg, fullCommand()))
	first := must(r.commands.Due(bg, t0, 10))(t)
	ok(t, r.commands.Save(bg, retried(first[0])))
	later := must(r.commands.Due(bg, t0.Add(time.Hour), 10))(t)
	answer, found, err := r.idem.Lookup(bg, "k-9", t0)
	if err := r.idem.Save(bg, fullAnswer(), t0.Add(-24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	stored, storedFound, storedErr := r.idem.Lookup(bg, "k-9", t0)
	return []any{first, later, fmt.Sprint(answer.Key, found, err), stored, storedFound, storedErr}
}

func TestCommandsAndAnswersKeepEveryField(t *testing.T) {
	c := fullCommand()
	c.Seq = 1
	nonZero(t, "command.Command", reflect.ValueOf(c))
	nonZero(t, "idempotency.IdempotencyRecord", reflect.ValueOf(fullAnswer()))
	want := []any{[]command.Command{c}, []command.Command{retried(c)}, fmt.Sprint("", false, nil), fullAnswer(), true, nil}
	oracle(t, "command and answer round trip", want, commandRoundTrip)
}

func openOrderIDs(t *testing.T, r repos) any {
	for _, id := range []string{"to-b", "to-c", "to-a"} { // one acceptance instant: the tie keeps insertion order
		s := fullOrder(id)
		if id == "to-c" {
			s.Status = to.StatusCompleted
		}
		ok(t, r.orders.Insert(bg, to.Restore(s)))
	}
	var ids []string
	for _, o := range must(r.orders.Open(bg))(t) {
		ids = append(ids, o.ID())
	}
	return ids
}

func listedKeys(t *testing.T, r repos) any {
	for _, id := range []string{"b2", "a1", "B1"} {
		ok(t, r.units.Save(bg, facts.EquipmentState{AsrsID: "ASRS1", UnitID: id, State: "IDLE", At: t0}))
	}
	for _, s := range []capacity.Snapshot{capacity.Station("b", 1, 0, 0, t0), capacity.Station("a", 1, 0, 0, t0),
		capacity.Asrs("ASRS1", 1, 1, 0, 60, t0), capacity.Station("B", 1, 0, 0, t0)} {
		ok(t, r.caps.Save(bg, s))
	}
	var keys []string
	for _, u := range must(r.units.List(bg, "ASRS1"))(t) {
		keys = append(keys, u.UnitID)
	}
	for _, s := range must(r.caps.List(bg))(t) {
		keys = append(keys, s.Subject())
	}
	return keys
}

func TestListsComeBackInTheMemoryDoublesOrder(t *testing.T) {
	oracle(t, "open orders", []string{"to-b", "to-a"}, openOrderIDs)
	oracle(t, "units then capacities, byte order", []string{"B1", "a1", "b2", "ASRS/ASRS1", "STATION/B", "STATION/a", "STATION/b"}, listedKeys)
}

func limits(t *testing.T, r repos) any {
	track := ct.New("TOTE-1", t0)
	track.MoveTo(onRobot, t0)
	ok(t, r.tracks.Save(bg, track))
	ok(t, r.commands.Enqueue(bg, command.New("c-1", "to-1", "to-1.1.1", command.RequestLeg, t0, time.Minute)))
	var out []string
	for _, limit := range []int{0, -1} {
		h, herr := r.tracks.History(bg, "TOTE-1", limit)
		d, derr := r.commands.Due(bg, t0, limit)
		out = append(out, fmt.Sprint(len(h), herr), fmt.Sprint(len(d), derr))
	}
	return out
}

func TestANonPositiveLimitListsNothing(t *testing.T) {
	oracle(t, "limits", []string{"0 <nil>", "0 <nil>", "0 <nil>", "0 <nil>"}, limits)
}

func unknownKeys(t *testing.T, r repos) any {
	_, e1 := r.orders.Get(bg, "nope")
	_, e2 := r.orders.ByLegRef(bg, "nope.1.1")
	_, e3 := r.tracks.Get(bg, "nope")
	_, e4 := r.stations.Get(bg, "nope")
	_, e5 := r.requests.Get(bg, "nope")
	_, found, e6 := r.caps.Get(bg, capacity.KindAsrs, "nope")
	ok(t, r.orders.Insert(bg, to.Restore(fullOrder("to-1"))))
	e7 := r.orders.Insert(bg, to.Restore(fullOrder("to-1")))
	h := must(r.tracks.History(bg, "nope", 10))(t)
	u := must(r.units.List(bg, "nope"))(t)
	return []any{errors.Is(e1, ports.ErrNotFound), errors.Is(e2, ports.ErrNotFound), errors.Is(e3, ports.ErrNotFound),
		errors.Is(e4, ports.ErrNotFound), errors.Is(e5, ports.ErrNotFound), found, e6, errors.Is(e7, ports.ErrDuplicate), len(h), len(u)}
}

func TestUnknownKeysAndDuplicatesAnswerAsTheMemoryDoubles(t *testing.T) {
	oracle(t, "unknown keys", []any{true, true, true, true, true, false, nil, true, 0, 0}, unknownKeys)
}

func answerWindow(t *testing.T, r repos) any {
	first, second := fullAnswer(), fullAnswer()
	second.Fingerprint, second.CreatedAt = "f-other", t0.Add(time.Hour)
	ok(t, r.idem.Save(bg, first, t0.Add(-time.Hour)))
	ok(t, r.idem.Save(bg, second, t0)) // the first row is inside the window: the first writer keeps the key
	kept, _, _ := r.idem.Lookup(bg, "k-9", t0)
	_, atBoundary, _ := r.idem.Lookup(bg, "k-9", t0)
	_, pastBoundary, _ := r.idem.Lookup(bg, "k-9", t0.Add(time.Microsecond))
	ok(t, r.idem.Save(bg, second, t0.Add(time.Microsecond))) // older than notBefore: replaced
	replaced, _, _ := r.idem.Lookup(bg, "k-9", t0)
	none := must(r.idem.Purge(bg, t0.Add(time.Hour)))(t)
	one := must(r.idem.Purge(bg, t0.Add(time.Hour+time.Microsecond)))(t)
	return []any{kept.Fingerprint, atBoundary, pastBoundary, replaced.Fingerprint, none, one}
}

func TestTheAnswerWindowMatchesTheMemoryDouble(t *testing.T) {
	oracle(t, "answer window", []any{"f-9", true, false, "f-other", int64(0), int64(1)}, answerWindow)
}
