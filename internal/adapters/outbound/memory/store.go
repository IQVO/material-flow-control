// Package memory holds the in-memory test doubles of every application port. Tests only: no cmd/ imports it
// (an internal/architecture test enforces that).
package memory

import (
	"context"
	"errors"
	"sync"

	"github.com/IQVO/material-flow-control/internal/application/command"
	"github.com/IQVO/material-flow-control/internal/application/facts"
	"github.com/IQVO/material-flow-control/internal/application/idempotency"
	"github.com/IQVO/material-flow-control/internal/domain/capacity"
	"github.com/IQVO/material-flow-control/internal/domain/containertrack"
	"github.com/IQVO/material-flow-control/internal/domain/stationqueue"
	"github.com/IQVO/material-flow-control/internal/domain/transportorder"
	"github.com/IQVO/material-flow-control/internal/domain/transportrequest"
)

// ErrInjected is returned by a repository write armed with FailNext.
var ErrInjected = errors.New("injected failure")

type tables struct {
	requests   map[string]transportrequest.Record
	orders     map[string]transportorder.Snapshot
	orderIDs   []string
	refs       map[string]string
	tracks     map[string]containertrack.Snapshot
	moves      map[string][]containertrack.Move
	stations   map[string]stationqueue.Snapshot
	capacities map[string]capacity.Snapshot
	units      map[string]facts.EquipmentState
	commands   []command.Command
	inbox      map[string]string
	events     []any
	idem       map[string]idempotency.IdempotencyRecord
	tags       map[string]int
	seq        int64
}

func newTables() tables {
	return tables{requests: map[string]transportrequest.Record{}, orders: map[string]transportorder.Snapshot{}, refs: map[string]string{},
		tracks: map[string]containertrack.Snapshot{}, moves: map[string][]containertrack.Move{}, stations: map[string]stationqueue.Snapshot{},
		capacities: map[string]capacity.Snapshot{}, units: map[string]facts.EquipmentState{}, inbox: map[string]string{},
		idem: map[string]idempotency.IdempotencyRecord{}, tags: map[string]int{}}
}

func cloneMap[K comparable, V any](m map[K]V) map[K]V {
	out := make(map[K]V, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func (t tables) clone() tables {
	c := t
	c.requests, c.orders, c.refs, c.tracks = cloneMap(t.requests), cloneMap(t.orders), cloneMap(t.refs), cloneMap(t.tracks)
	c.stations, c.capacities, c.units, c.inbox = cloneMap(t.stations), cloneMap(t.capacities), cloneMap(t.units), cloneMap(t.inbox)
	c.idem, c.tags = cloneMap(t.idem), cloneMap(t.tags)
	c.moves = make(map[string][]containertrack.Move, len(t.moves))
	for k, v := range t.moves {
		c.moves[k] = append([]containertrack.Move(nil), v...)
	}
	c.orderIDs = append([]string(nil), t.orderIDs...)
	c.commands = append([]command.Command(nil), t.commands...)
	c.events = append([]any(nil), t.events...)
	return c
}

// Store is one in-memory database shared by the repositories built on it.
type Store struct {
	mu     sync.Mutex
	tx     sync.Mutex
	t      tables
	failOn map[string]bool
}

// NewStore is an empty store.
func NewStore() *Store { return &Store{t: newTables(), failOn: map[string]bool{}} }

func (s *Store) with(fn func(t *tables)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.t)
}

// FailNext arms the next write of repo ("orders", "tracks", "stations", "commands", "events", "requests",
// "capacities") to fail with ErrInjected (atomicity tests).
func (s *Store) FailNext(repo string) { s.with(func(*tables) { s.failOn[repo] = true }) }

func (s *Store) injected(repo string) error {
	var err error
	s.with(func(*tables) {
		if s.failOn[repo] {
			delete(s.failOn, repo)
			err = ErrInjected
		}
	})
	return err
}

// Tag records one tagged write in the caller's unit of work (the replay-class template's probe).
func (s *Store) Tag(_ context.Context, tag string) error {
	s.with(func(t *tables) { t.tags[tag]++ })
	return nil
}

// Tagged counts committed writes tagged tag.
func (s *Store) Tagged(tag string) int {
	n := 0
	s.with(func(t *tables) { n = t.tags[tag] })
	return n
}

type txKey struct{}

type txState struct{ hooks []func() }

// UnitOfWork serializes units of work on the store and restores every table when fn fails or panics.
type UnitOfWork struct{ s *Store }

// NewUnitOfWork builds the memory ports.UnitOfWork.
func NewUnitOfWork(s *Store) *UnitOfWork { return &UnitOfWork{s: s} }

// Do runs fn atomically; a nested Do joins the outer one.
func (u *UnitOfWork) Do(ctx context.Context, fn func(ctx context.Context) error) (err error) {
	if _, ok := ctx.Value(txKey{}).(*txState); ok {
		return fn(ctx)
	}
	u.s.tx.Lock()
	defer u.s.tx.Unlock()
	var saved tables
	u.s.with(func(t *tables) { saved = t.clone() })
	st := &txState{}
	defer func() {
		if r := recover(); r != nil {
			u.s.with(func(t *tables) { *t = saved })
			panic(r)
		}
	}()
	if err = fn(context.WithValue(ctx, txKey{}, st)); err != nil {
		u.s.with(func(t *tables) { *t = saved })
		return err
	}
	for _, h := range st.hooks {
		h()
	}
	return nil
}

// AfterCommit registers fn on the outermost unit of work, or runs it at once outside one.
func (u *UnitOfWork) AfterCommit(ctx context.Context, fn func()) {
	if st, ok := ctx.Value(txKey{}).(*txState); ok {
		st.hooks = append(st.hooks, fn)
		return
	}
	fn()
}
