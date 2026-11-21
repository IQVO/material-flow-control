package memory

import (
	"context"
	"sort"
	"time"

	"github.com/IQVO/material-flow-control/internal/application/command"
	"github.com/IQVO/material-flow-control/internal/application/facts"
	"github.com/IQVO/material-flow-control/internal/application/ports"
	"github.com/IQVO/material-flow-control/internal/domain/capacity"
	"github.com/IQVO/material-flow-control/internal/domain/containertrack"
	"github.com/IQVO/material-flow-control/internal/domain/stationqueue"
	"github.com/IQVO/material-flow-control/internal/domain/transportorder"
	"github.com/IQVO/material-flow-control/internal/domain/transportrequest"
)

type Requests struct{ s *Store }

func NewRequests(s *Store) *Requests { return &Requests{s: s} }

func (r *Requests) Get(_ context.Context, id string) (transportrequest.Record, error) {
	var rec transportrequest.Record
	var ok bool
	r.s.with(func(t *tables) { rec, ok = t.requests[id] })
	if !ok {
		return rec, ports.ErrNotFound
	}
	return rec, nil
}

func (r *Requests) Insert(_ context.Context, rec transportrequest.Record) error {
	if err := r.s.injected("requests"); err != nil {
		return err
	}
	var err error
	r.s.with(func(t *tables) {
		if _, dup := t.requests[rec.Request.ID]; dup {
			err = ports.ErrDuplicate
			return
		}
		t.requests[rec.Request.ID] = rec
	})
	return err
}

type Orders struct{ s *Store }

func NewOrders(s *Store) *Orders { return &Orders{s: s} }

func (r *Orders) Get(_ context.Context, id string) (*transportorder.Order, error) {
	var snap transportorder.Snapshot
	var ok bool
	r.s.with(func(t *tables) { snap, ok = t.orders[id] })
	if !ok {
		return nil, ports.ErrNotFound
	}
	return transportorder.Restore(snap), nil
}

func (r *Orders) ByLegRef(ctx context.Context, ref string) (*transportorder.Order, error) {
	var id string
	r.s.with(func(t *tables) { id = t.refs[ref] })
	if id == "" {
		return nil, ports.ErrNotFound
	}
	return r.Get(ctx, id)
}

func (r *Orders) Open(_ context.Context) ([]*transportorder.Order, error) {
	var out []*transportorder.Order
	r.s.with(func(t *tables) {
		for _, id := range t.orderIDs {
			if s := t.orders[id]; s.Status == transportorder.StatusInProgress {
				out = append(out, transportorder.Restore(s))
			}
		}
	})
	return out, nil
}

func (r *Orders) put(t *tables, o *transportorder.Order) {
	s := o.Snapshot()
	s.Version++
	t.orders[s.ID] = s
	for _, l := range s.Legs {
		t.refs[l.Ref] = s.ID
	}
}

func (r *Orders) Insert(_ context.Context, o *transportorder.Order) error {
	if err := r.s.injected("orders"); err != nil {
		return err
	}
	var err error
	r.s.with(func(t *tables) {
		if _, dup := t.orders[o.ID()]; dup {
			err = ports.ErrDuplicate
			return
		}
		t.orderIDs = append(t.orderIDs, o.ID())
		r.put(t, o)
	})
	return err
}

func (r *Orders) Save(_ context.Context, o *transportorder.Order) error {
	if err := r.s.injected("orders"); err != nil {
		return err
	}
	r.s.with(func(t *tables) { r.put(t, o) })
	return nil
}

type Tracks struct{ s *Store }

func NewTracks(s *Store) *Tracks { return &Tracks{s: s} }

func (r *Tracks) Get(_ context.Context, id string) (*containertrack.Track, error) {
	var snap containertrack.Snapshot
	var ok bool
	r.s.with(func(t *tables) { snap, ok = t.tracks[id] })
	if !ok {
		return nil, ports.ErrNotFound
	}
	return containertrack.Restore(snap), nil
}

func (r *Tracks) Save(_ context.Context, tr *containertrack.Track) error {
	if err := r.s.injected("tracks"); err != nil {
		return err
	}
	moves := tr.PullMoves()
	r.s.with(func(t *tables) {
		s := tr.Snapshot()
		s.Version++
		t.tracks[s.ContainerID] = s
		t.moves[s.ContainerID] = append(t.moves[s.ContainerID], moves...)
	})
	return nil
}

func (r *Tracks) History(_ context.Context, id string, limit int) ([]containertrack.Move, error) {
	var out []containertrack.Move
	r.s.with(func(t *tables) {
		m := t.moves[id]
		for i := len(m) - 1; i >= 0 && len(out) < limit; i-- {
			out = append(out, m[i])
		}
	})
	return out, nil
}

type Stations struct{ s *Store }

func NewStations(s *Store) *Stations { return &Stations{s: s} }

func (r *Stations) Get(_ context.Context, id string) (*stationqueue.Queue, error) {
	var snap stationqueue.Snapshot
	var ok bool
	r.s.with(func(t *tables) { snap, ok = t.stations[id] })
	if !ok {
		return nil, ports.ErrNotFound
	}
	return stationqueue.Restore(snap), nil
}

func (r *Stations) Save(_ context.Context, q *stationqueue.Queue) error {
	if err := r.s.injected("stations"); err != nil {
		return err
	}
	r.s.with(func(t *tables) {
		s := q.Snapshot()
		s.Version++
		t.stations[s.StationID] = s
	})
	return nil
}

type Capacities struct{ s *Store }

func NewCapacities(s *Store) *Capacities { return &Capacities{s: s} }

func capKey(k capacity.Kind, id string) string { return string(k) + "/" + id }

func (r *Capacities) Get(_ context.Context, k capacity.Kind, id string) (capacity.Snapshot, bool, error) {
	var snap capacity.Snapshot
	var ok bool
	r.s.with(func(t *tables) { snap, ok = t.capacities[capKey(k, id)] })
	return snap, ok, nil
}

func (r *Capacities) Save(_ context.Context, s capacity.Snapshot) error {
	if err := r.s.injected("capacities"); err != nil {
		return err
	}
	r.s.with(func(t *tables) { t.capacities[capKey(s.Kind, s.ID)] = s })
	return nil
}

func (r *Capacities) List(_ context.Context) ([]capacity.Snapshot, error) {
	var out []capacity.Snapshot
	r.s.with(func(t *tables) {
		for _, s := range t.capacities {
			out = append(out, s)
		}
	})
	sort.Slice(out, func(i, j int) bool { return capKey(out[i].Kind, out[i].ID) < capKey(out[j].Kind, out[j].ID) })
	return out, nil
}

type Units struct{ s *Store }

func NewUnits(s *Store) *Units { return &Units{s: s} }

func (r *Units) Save(_ context.Context, u facts.EquipmentState) error {
	r.s.with(func(t *tables) { t.units[u.AsrsID+"/"+u.UnitID] = u })
	return nil
}

func (r *Units) List(_ context.Context, asrsID string) ([]facts.EquipmentState, error) {
	var out []facts.EquipmentState
	r.s.with(func(t *tables) {
		for _, u := range t.units {
			if u.AsrsID == asrsID {
				out = append(out, u)
			}
		}
	})
	sort.Slice(out, func(i, j int) bool { return out[i].UnitID < out[j].UnitID })
	return out, nil
}

type Commands struct{ s *Store }

func NewCommands(s *Store) *Commands { return &Commands{s: s} }

func (r *Commands) Enqueue(_ context.Context, c command.Command) error {
	if err := r.s.injected("commands"); err != nil {
		return err
	}
	r.s.with(func(t *tables) {
		t.seq++
		c.Seq = t.seq
		t.commands = append(t.commands, c)
	})
	return nil
}

func (r *Commands) Due(_ context.Context, now time.Time, limit int) ([]command.Command, error) {
	var out []command.Command
	r.s.with(func(t *tables) {
		for _, c := range t.commands {
			if c.Status == command.StatusPending && !c.NextAttemptAt.After(now) && len(out) < limit {
				out = append(out, c)
			}
		}
	})
	return out, nil
}

func (r *Commands) Save(_ context.Context, c command.Command) error {
	r.s.with(func(t *tables) {
		for i := range t.commands {
			if t.commands[i].ID == c.ID {
				t.commands[i] = c
			}
		}
	})
	return nil
}

// All returns every command in creation order.
func (r *Commands) All() []command.Command {
	var out []command.Command
	r.s.with(func(t *tables) { out = append(out, t.commands...) })
	return out
}

type Inbox struct{ s *Store }

func NewInbox(s *Store) *Inbox { return &Inbox{s: s} }

func (r *Inbox) Claim(_ context.Context, id, typ string, _ time.Time) (bool, error) {
	fresh := false
	r.s.with(func(t *tables) {
		if _, seen := t.inbox[id]; !seen {
			t.inbox[id], fresh = typ, true
		}
	})
	return fresh, nil
}

type Events struct{ s *Store }

func NewEvents(s *Store) *Events { return &Events{s: s} }

func (r *Events) Append(_ context.Context, evs ...any) error {
	if err := r.s.injected("events"); err != nil {
		return err
	}
	r.s.with(func(t *tables) { t.events = append(t.events, evs...) })
	return nil
}

// All returns every committed event in order.
func (r *Events) All() []any {
	var out []any
	r.s.with(func(t *tables) { out = append(out, t.events...) })
	return out
}

// Reset forgets the committed events.
func (r *Events) Reset() { r.s.with(func(t *tables) { t.events = nil }) }
