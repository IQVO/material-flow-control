// Package transportrequest is the WES's transport request and its ONE recorded outcome (responsibility 6,
// decision D7): accepted with a transport order, or rejected with a reason.
package transportrequest

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
)

// ErrInvalid is wrapped by every request validation error.
var ErrInvalid = errors.New("invalid transport request")

// Purpose is why a container moves (seam decision 4).
type Purpose string

const (
	PurposePick      Purpose = "PICK"
	PurposePutaway   Purpose = "PUTAWAY"
	PurposeReplenish Purpose = "REPLENISH"
	PurposeOutbound  Purpose = "OUTBOUND"
	PurposeReturn    Purpose = "RETURN"
)

// Priority is the WES priority band.
type Priority string

const (
	PriorityNormal Priority = "NORMAL"
	PriorityUrgent Priority = "URGENT"
)

// Rank orders priorities: a lower rank is served first.
func (p Priority) Rank() int {
	if p == PriorityUrgent {
		return 0
	}
	return 1
}

// Reason is a rejection reason (seam decision 3).
type Reason string

const (
	ReasonUnknownContainer       Reason = "UNKNOWN_CONTAINER"
	ReasonContainerBusy          Reason = "CONTAINER_BUSY"
	ReasonDestinationUnreachable Reason = "DESTINATION_UNREACHABLE"
	ReasonSubsystemUnavailable   Reason = "SUBSYSTEM_UNAVAILABLE"
)

const (
	maxIDLen        = 128
	maxContainerLen = 64
)

// Request is one transport request as the WES sent it.
type Request struct {
	ID          string
	ContainerID string
	Source      string
	Destination string
	Purpose     Purpose
	Priority    Priority
	DueBy       time.Time
}

func invalid(field, rule string) error { return fmt.Errorf("%w: %s %s", ErrInvalid, field, rule) }

func checkID(field, v string) error {
	if v == "" || len([]rune(v)) > maxIDLen {
		return invalid(field, "must be 1 to 128 characters")
	}
	if strings.IndexFunc(v, unicode.IsControl) >= 0 {
		return invalid(field, "must not contain control characters")
	}
	return nil
}

// lpnChar is the inventory-storage LPN alphabet (plan 05a D1: ^[A-Za-z0-9._-]{1,64}$), byte by byte: every byte of a
// non-ASCII rune is >= 0x80 and so outside it. Spelled as the regexp's ranges (Task 26 mutation work: the former
// `r < unicode.MaxASCII` guard had an equivalent boundary mutant, DEL being no letter or digit); same alphabet.
func lpnChar(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || c == '.' || c == '_' || c == '-'
}

func checkContainer(v string) error {
	if v == "" || len(v) > maxContainerLen {
		return invalid("container_id", "must match ^[A-Za-z0-9._-]{1,64}$")
	}
	for i := range len(v) {
		if !lpnChar(v[i]) {
			return invalid("container_id", "must match ^[A-Za-z0-9._-]{1,64}$")
		}
	}
	return nil
}

// Validate checks every field against the relationship-1 contract.
func (r Request) Validate() error {
	for _, f := range []struct{ name, v string }{{"transport_request_id", r.ID}, {"source", r.Source}, {"destination", r.Destination}} {
		if err := checkID(f.name, f.v); err != nil {
			return err
		}
	}
	if err := checkContainer(r.ContainerID); err != nil {
		return err
	}
	switch r.Purpose {
	case PurposePick, PurposePutaway, PurposeReplenish, PurposeOutbound, PurposeReturn:
	default:
		return invalid("purpose", "must be PICK, PUTAWAY, REPLENISH, OUTBOUND or RETURN")
	}
	if r.Priority != PriorityNormal && r.Priority != PriorityUrgent {
		return invalid("priority", "must be NORMAL or URGENT")
	}
	if r.DueBy.IsZero() {
		return invalid("due_by", "is required")
	}
	return nil
}

// Normalized returns r with DueBy in UTC at microsecond precision (what Postgres keeps).
func (r Request) Normalized() Request {
	r.DueBy = r.DueBy.UTC().Truncate(time.Microsecond)
	return r
}

// Fingerprint identifies the request's content (id excluded): the same id with another fingerprint is a conflict.
func (r Request) Fingerprint() string {
	n := r.Normalized()
	return strings.Join([]string{n.ContainerID, n.Source, n.Destination, string(n.Purpose), string(n.Priority),
		n.DueBy.Format(time.RFC3339Nano)}, "\x1f")
}

// Outcome is the request's one outcome.
type Outcome string

const (
	OutcomeAccepted Outcome = "ACCEPTED"
	OutcomeRejected Outcome = "REJECTED"
)

// Record is the request with its outcome; it never changes once written.
type Record struct {
	Request Request
	Outcome Outcome
	OrderID string
	Reason  Reason
	Detail  string
	At      time.Time
}

// SameRequest reports whether req carries the recorded request's content.
func (r Record) SameRequest(req Request) bool { return r.Request.Fingerprint() == req.Fingerprint() }

// Accept records an accepted request.
func Accept(req Request, orderID string, at time.Time) Record {
	return Record{Request: req, Outcome: OutcomeAccepted, OrderID: orderID, At: at}
}

// TransportRejected is published for every rejection (entity segment transportrequest).
type TransportRejected struct {
	RequestID   string
	ContainerID string
	Source      string
	Destination string
	Purpose     Purpose
	Reason      Reason
	Detail      string
	At          time.Time
}

// Reject records a rejected request and returns its event.
func Reject(req Request, reason Reason, detail string, at time.Time) (Record, TransportRejected) {
	return Record{Request: req, Outcome: OutcomeRejected, Reason: reason, Detail: detail, At: at},
		TransportRejected{RequestID: req.ID, ContainerID: req.ContainerID, Source: req.Source, Destination: req.Destination,
			Purpose: req.Purpose, Reason: reason, Detail: detail, At: at}
}
