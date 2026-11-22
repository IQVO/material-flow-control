// Package command is one queued call to a subsystem (ADR 0002 decision 4, the command outbox): written in the
// unit of work that decided it, executed by the dispatcher outside any transaction.
package command

import "time"

// Kind is what the dispatcher calls.
type Kind string

const (
	RequestLeg    Kind = "REQUEST_LEG"
	CancelLeg     Kind = "CANCEL_LEG"
	ConfirmPickup Kind = "CONFIRM_PICKUP"
)

// Status is the command's state.
type Status string

const (
	StatusPending Status = "PENDING"
	StatusDone    Status = "DONE"
	StatusFailed  Status = "FAILED"
)

// Command is one subsystem call. Key is the Idempotency-Key, stable across retries; Seq (assigned by the store)
// is the creation order the dispatcher follows.
type Command struct {
	ID            string
	Seq           int64
	OrderID       string
	LegRef        string
	Kind          Kind
	Key           string
	Status        Status
	Attempts      int
	CreatedAt     time.Time
	NextAttemptAt time.Time
	Deadline      time.Time
	LastError     string
}

func key(kind Kind, legRef string) string {
	switch kind {
	case CancelLeg:
		return "cancel-" + legRef
	case ConfirmPickup:
		return "pickup-" + legRef
	default:
		return legRef
	}
}

// New is a pending command due now, retried until now+window.
func New(id, orderID, legRef string, kind Kind, now time.Time, window time.Duration) Command {
	return Command{ID: id, OrderID: orderID, LegRef: legRef, Kind: kind, Key: key(kind, legRef), Status: StatusPending,
		CreatedAt: now, NextAttemptAt: now, Deadline: now.Add(window)}
}

// Backoff is initial * 2^(attempt-1), capped at maxDelay.
func Backoff(attempt int, initial, maxDelay time.Duration) time.Duration {
	d := initial
	for i := 1; i < attempt && d < maxDelay; i++ {
		d *= 2
	}
	return min(d, maxDelay)
}

// Retry schedules the next attempt.
func (c Command) Retry(now time.Time, initial, maxDelay time.Duration, errText string) Command {
	c.Attempts++
	c.NextAttemptAt = now.Add(Backoff(c.Attempts, initial, maxDelay))
	c.LastError = errText
	return c
}

// Expired reports a command whose retry window is over.
func (c Command) Expired(now time.Time) bool { return !now.Before(c.Deadline) }

// Done marks the command executed.
func (c Command) Done(now time.Time) Command {
	c.Attempts++
	c.Status, c.NextAttemptAt = StatusDone, now
	return c
}

// Failed marks the command given up.
func (c Command) Failed(now time.Time, errText string) Command {
	c.Attempts++
	c.Status, c.NextAttemptAt, c.LastError = StatusFailed, now, errText
	return c
}
