// Package stationqueue is the StationQueue aggregate (responsibility 3, decision D11): the occupants of one
// goods-to-person station and the arrivals waiting upstream for a slot. Occupants never exceed the buffer limit;
// waiting entries are admitted URGENT first, then by due time, then by readiness, then by order id.
package stationqueue

import (
	"cmp"
	"slices"
	"strings"
	"time"

	"github.com/IQVO/material-flow-control/internal/domain/transportrequest"
)

// Entry is one arrival waiting upstream.
type Entry struct {
	OrderID     string
	RequestID   string
	ContainerID string
	Priority    transportrequest.Priority
	DueBy       time.Time
	ReadyAt     time.Time
}

// Occupant holds one buffer slot, from admission until the container leaves the station.
type Occupant struct {
	OrderID     string
	RequestID   string
	ContainerID string
	Seq         int64
	AdmittedAt  time.Time
}

// Snapshot is the persisted state.
type Snapshot struct {
	StationID   string
	BufferLimit int
	Occupants   []Occupant
	Waiting     []Entry
	NextSeq     int64
	Version     int64
}

// Queue is one station's queue.
type Queue struct{ s Snapshot }

// New is the empty queue of a station.
func New(stationID string, bufferLimit int) *Queue {
	return &Queue{s: Snapshot{StationID: stationID, BufferLimit: bufferLimit, NextSeq: 1}}
}

func clone(s Snapshot) Snapshot {
	s.Occupants = append([]Occupant(nil), s.Occupants...)
	s.Waiting = append([]Entry(nil), s.Waiting...)
	return s
}

// Restore rebuilds a queue from its snapshot.
func Restore(s Snapshot) *Queue { return &Queue{s: clone(s)} }

// Snapshot returns a copy of the state.
func (q *Queue) Snapshot() Snapshot { return clone(q.s) }

func (q *Queue) StationID() string     { return q.s.StationID }
func (q *Queue) BufferLimit() int      { return q.s.BufferLimit }
func (q *Queue) Occupied() int         { return len(q.s.Occupants) }
func (q *Queue) Version() int64        { return q.s.Version }
func (q *Queue) Occupants() []Occupant { return append([]Occupant(nil), q.s.Occupants...) }

// SetBufferLimit applies the configured limit; occupants above a lowered limit stay until they leave.
func (q *Queue) SetBufferLimit(n int) { q.s.BufferLimit = n }

// compare is the admission order (decision D11): URGENT before NORMAL, then earlier due_by, then earlier readiness,
// then order id. Written as one comparison chain with no relational operator, so no boundary mutant is equivalent
// (Task 26 mutation work; the order is the brief's less, unchanged).
func compare(a, b Entry) int {
	return cmp.Or(cmp.Compare(a.Priority.Rank(), b.Priority.Rank()), a.DueBy.Compare(b.DueBy), a.ReadyAt.Compare(b.ReadyAt),
		strings.Compare(a.OrderID, b.OrderID))
}

// Waiting returns the waiting entries in admission order.
func (q *Queue) Waiting() []Entry {
	w := append([]Entry(nil), q.s.Waiting...)
	slices.SortStableFunc(w, compare)
	return w
}

func (q *Queue) known(orderID string) bool {
	for _, e := range q.s.Waiting {
		if e.OrderID == orderID {
			return true
		}
	}
	for _, o := range q.s.Occupants {
		if o.OrderID == orderID {
			return true
		}
	}
	return false
}

// Join adds a ready arrival; an order already waiting or admitted is not added twice.
func (q *Queue) Join(e Entry) bool {
	if q.known(e.OrderID) {
		return false
	}
	q.s.Waiting = append(q.s.Waiting, e)
	return true
}

// Admit grants free slots to the best waiting entries and returns the new occupants in admission order.
func (q *Queue) Admit(at time.Time) []Occupant {
	w := q.Waiting()
	var admitted []Occupant
	for len(w) > 0 && len(q.s.Occupants) < q.s.BufferLimit {
		e := w[0]
		w = w[1:]
		o := Occupant{OrderID: e.OrderID, RequestID: e.RequestID, ContainerID: e.ContainerID, Seq: q.s.NextSeq, AdmittedAt: at}
		q.s.NextSeq++
		q.s.Occupants = append(q.s.Occupants, o)
		admitted = append(admitted, o)
	}
	q.s.Waiting = w
	return admitted
}

// Leave frees the slot of a container that left the station.
func (q *Queue) Leave(containerID string) bool {
	for i, o := range q.s.Occupants {
		if o.ContainerID == containerID {
			q.s.Occupants = append(q.s.Occupants[:i], q.s.Occupants[i+1:]...)
			return true
		}
	}
	return false
}

// Drop removes an order's waiting entry or frees its slot (its inbound transport ended before the arrival).
func (q *Queue) Drop(orderID string) bool {
	for i, e := range q.s.Waiting {
		if e.OrderID == orderID {
			q.s.Waiting = append(q.s.Waiting[:i], q.s.Waiting[i+1:]...)
			return true
		}
	}
	for i, o := range q.s.Occupants {
		if o.OrderID == orderID {
			q.s.Occupants = append(q.s.Occupants[:i], q.s.Occupants[i+1:]...)
			return true
		}
	}
	return false
}

// Position is a container's 1-based place among the occupants in admission order; 0 when absent.
func (q *Queue) Position(containerID string) int {
	occ := q.Occupants()
	slices.SortFunc(occ, func(a, b Occupant) int { return cmp.Compare(a.Seq, b.Seq) }) // unique Seq
	for i, o := range occ {
		if o.ContainerID == containerID {
			return i + 1
		}
	}
	return 0
}
