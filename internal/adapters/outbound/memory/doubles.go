package memory

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/IQVO/material-flow-control/internal/application/idempotency"
)

// Clock is a settable clock.
type Clock struct {
	mu  sync.Mutex
	now time.Time
}

func NewClock(t time.Time) *Clock { return &Clock{now: t} }

func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func (c *Clock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

// IDs mints prefix-1, prefix-2, …
type IDs struct {
	prefix string
	n      atomic.Int64
}

func NewIDs(prefix string) *IDs { return &IDs{prefix: prefix} }

func (i *IDs) NewID() string { return fmt.Sprintf("%s-%d", i.prefix, i.n.Add(1)) }

// FlowLock is a no-op: the memory unit of work is already serial.
type FlowLock struct{}

func (FlowLock) Lock(context.Context) error { return nil }

// Kicker counts kicks.
type Kicker struct{ n atomic.Int64 }

func (k *Kicker) Kick()        { k.n.Add(1) }
func (k *Kicker) Count() int64 { return k.n.Load() }

// Ended is one recorded TransportEnded call.
type Ended struct {
	Purpose, Outcome, Reason string
	Took                     time.Duration
}

// Metrics records TransportEnded calls.
type Metrics struct {
	mu    sync.Mutex
	ended []Ended
}

func (m *Metrics) TransportEnded(_ context.Context, purpose, outcome, reason string, took time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ended = append(m.ended, Ended{Purpose: purpose, Outcome: outcome, Reason: reason, Took: took})
}

func (m *Metrics) Ended() []Ended {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Ended(nil), m.ended...)
}

// Idempotency is the memory ports.IdempotencyStore.
type Idempotency struct{ s *Store }

func NewIdempotency(s *Store) *Idempotency { return &Idempotency{s: s} }

func (i *Idempotency) Lookup(_ context.Context, key string, notBefore time.Time) (idempotency.IdempotencyRecord, bool, error) {
	var rec idempotency.IdempotencyRecord
	var ok bool
	i.s.with(func(t *tables) { rec, ok = t.idem[key] })
	if !ok || rec.CreatedAt.Before(notBefore) {
		return idempotency.IdempotencyRecord{}, false, nil
	}
	return rec, true, nil
}

func (i *Idempotency) Save(_ context.Context, rec idempotency.IdempotencyRecord, notBefore time.Time) error {
	i.s.with(func(t *tables) {
		if old, ok := t.idem[rec.Key]; ok && !old.CreatedAt.Before(notBefore) {
			return
		}
		t.idem[rec.Key] = rec
	})
	return nil
}

func (i *Idempotency) Purge(_ context.Context, before time.Time) (int64, error) {
	var n int64
	i.s.with(func(t *tables) {
		for k, rec := range t.idem {
			if rec.CreatedAt.Before(before) {
				delete(t.idem, k)
				n++
			}
		}
	})
	return n, nil
}

// Rows counts the stored answers for key (the replay-class wiring's KeyRows).
func (i *Idempotency) Rows(key string) int {
	n := 0
	i.s.with(func(t *tables) {
		if _, ok := t.idem[key]; ok {
			n = 1
		}
	})
	return n
}
