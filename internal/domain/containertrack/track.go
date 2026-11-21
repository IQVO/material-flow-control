// Package containertrack is the ContainerTrack aggregate: the physical position of one container inside the
// automation and its history (capability "Container tracking"), at most one active transport order
// (responsibility 1) and the clarification hold (responsibility 4).
package containertrack

import (
	"errors"
	"time"

	"github.com/IQVO/material-flow-control/internal/domain/layout"
)

var (
	ErrBusy               = errors.New("the container has an active transport order")
	ErrInClarification    = errors.New("the container is held at clarification")
	ErrNotInClarification = errors.New("the container is not in clarification")
)

// PositionKind is where a container is.
type PositionKind string

const (
	InAsrs          PositionKind = "IN_ASRS"
	AtPort          PositionKind = "AT_PORT"
	OnRobot         PositionKind = "ON_ROBOT"
	AtStation       PositionKind = "AT_STATION"
	AtHandoff       PositionKind = "AT_HANDOFF"
	AtClarification PositionKind = "AT_CLARIFICATION"
	Unknown         PositionKind = "UNKNOWN"
)

// Position is a container's physical position: Code is a place code (or the fleet id when ON_ROBOT); Robot is
// the robot key, only ever shown in the read-only views.
type Position struct {
	Kind  PositionKind
	Code  string
	Robot string
}

// Inside reports a position only the automation can take a container out of.
func (p Position) Inside() bool { return p.Kind == InAsrs || p.Kind == AtPort || p.Kind == OnRobot }

// At is the position of a container standing at place p.
func At(p layout.Place) Position {
	kinds := map[layout.PlaceKind]PositionKind{layout.PlaceAsrs: InAsrs, layout.PlacePort: AtPort,
		layout.PlaceStation: AtStation, layout.PlaceClarification: AtClarification}
	k, ok := kinds[p.Kind]
	if !ok {
		k = AtHandoff
	}
	return Position{Kind: k, Code: p.Code}
}

// OnRobotOf is the position of a container on a robot of a fleet.
func OnRobotOf(fleetID, robot string) Position {
	return Position{Kind: OnRobot, Code: fleetID, Robot: robot}
}

// ClarificationReason says why a container was sent to clarification (decision D12).
type ClarificationReason string

const (
	ReasonNoRead             ClarificationReason = "NO_READ"
	ReasonMismatch           ClarificationReason = "MISMATCH"
	ReasonNotAtSource        ClarificationReason = "CONTAINER_NOT_AT_SOURCE"
	ReasonNotAtPort          ClarificationReason = "CONTAINER_NOT_AT_PORT"
	ReasonStorageMapMismatch ClarificationReason = "STORAGE_MAP_MISMATCH"
	ReasonOnFailedRobot      ClarificationReason = "CONTAINER_ON_FAILED_ROBOT"
)

// Clarification is the hold on a container.
type Clarification struct {
	Reason             ClarificationReason
	OrderID            string
	RequestID          string
	ScannedContainerID string
	DetectedAt         string
	Since              time.Time
}

// Move is one change of position.
type Move struct {
	From      Position
	To        Position
	RequestID string
	At        time.Time
}

// ContainerSentToClarification is published when a container is put on hold (entity segment containertrack).
type ContainerSentToClarification struct {
	ContainerID        string
	OrderID            string
	RequestID          string
	Reason             ClarificationReason
	ScannedContainerID string
	DetectedAt         string
	ClarificationPoint string
	At                 time.Time
}

// Snapshot is the persisted state.
type Snapshot struct {
	ContainerID     string
	Position        Position
	ActiveOrderID   string
	ActiveRequestID string
	Clarification   *Clarification
	UpdatedAt       time.Time
	Version         int64
}

// Track is one container's track.
type Track struct {
	s     Snapshot
	moves []Move
}

// New starts the track of a container seen for the first time, at an unknown position.
func New(containerID string, at time.Time) *Track {
	return &Track{s: Snapshot{ContainerID: containerID, Position: Position{Kind: Unknown}, UpdatedAt: at}}
}

func cloneClarification(c *Clarification) *Clarification {
	if c == nil {
		return nil
	}
	cp := *c
	return &cp
}

// Restore rebuilds a track from its snapshot.
func Restore(s Snapshot) *Track {
	s.Clarification = cloneClarification(s.Clarification)
	return &Track{s: s}
}

// Snapshot returns a copy of the state.
func (t *Track) Snapshot() Snapshot {
	s := t.s
	s.Clarification = cloneClarification(s.Clarification)
	return s
}

func (t *Track) ContainerID() string     { return t.s.ContainerID }
func (t *Track) Position() Position      { return t.s.Position }
func (t *Track) ActiveOrderID() string   { return t.s.ActiveOrderID }
func (t *Track) ActiveRequestID() string { return t.s.ActiveRequestID }
func (t *Track) Version() int64          { return t.s.Version }

// Clarification returns the hold, if any.
func (t *Track) Clarification() (Clarification, bool) {
	if t.s.Clarification == nil {
		return Clarification{}, false
	}
	return *t.s.Clarification, true
}

// Dispatchable is nil when a new transport may take the container.
func (t *Track) Dispatchable() error {
	if t.s.Clarification != nil {
		return ErrInClarification
	}
	if t.s.ActiveOrderID != "" {
		return ErrBusy
	}
	return nil
}

// Reserve makes orderID the container's one active order (responsibilities 1 and 4).
func (t *Track) Reserve(orderID, requestID string, at time.Time) error {
	if err := t.Dispatchable(); err != nil {
		return err
	}
	t.s.ActiveOrderID, t.s.ActiveRequestID, t.s.UpdatedAt = orderID, requestID, at
	return nil
}

// ReserveInternal reserves the container for an internal clarification move, which may take a held container.
func (t *Track) ReserveInternal(orderID string, at time.Time) error {
	if t.s.ActiveOrderID != "" {
		return ErrBusy
	}
	t.s.ActiveOrderID, t.s.ActiveRequestID, t.s.UpdatedAt = orderID, "", at
	return nil
}

// Release ends orderID's reservation; another order's release changes nothing.
func (t *Track) Release(orderID string, at time.Time) {
	if t.s.ActiveOrderID != orderID {
		return
	}
	t.s.ActiveOrderID, t.s.ActiveRequestID, t.s.UpdatedAt = "", "", at
}

// MoveTo records a physical fact: the container is now at p.
func (t *Track) MoveTo(p Position, at time.Time) {
	if p == t.s.Position {
		return
	}
	t.moves = append(t.moves, Move{From: t.s.Position, To: p, RequestID: t.s.ActiveRequestID, At: at})
	t.s.Position, t.s.UpdatedAt = p, at
}

// SendToClarification puts the container on hold and returns the event.
// ruling P4-R5: a track already held keeps its first hold (reason and Since unchanged) and the zero event is
// returned, so one container raises one exception case.
func (t *Track) SendToClarification(c Clarification, at time.Time) ContainerSentToClarification {
	if t.s.Clarification != nil {
		return ContainerSentToClarification{}
	}
	c.Since = at
	t.s.Clarification, t.s.UpdatedAt = &c, at
	return ContainerSentToClarification{ContainerID: t.s.ContainerID, OrderID: c.OrderID, RequestID: c.RequestID, Reason: c.Reason,
		ScannedContainerID: c.ScannedContainerID, DetectedAt: c.DetectedAt, At: at}
}

// Resolve lifts the hold; p, when given, is where the container now is.
func (t *Track) Resolve(p *Position, at time.Time) error {
	if t.s.Clarification == nil {
		return ErrNotInClarification
	}
	t.s.Clarification, t.s.UpdatedAt = nil, at
	if p != nil {
		t.MoveTo(*p, at)
	}
	return nil
}

// PullMoves returns and clears the moves recorded since the last pull.
func (t *Track) PullMoves() []Move {
	m := t.moves
	t.moves = nil
	return m
}
