// Package subsystem holds the value types of the subsystem-controller interface (relationships 4 and 5) that
// the application's client ports speak: what is asked, what is answered, and how a refusal is classified.
package subsystem

import (
	"errors"
	"fmt"
)

// ErrUnreachable is a transport error, a timeout or a 5xx: the call may be retried under the same key.
var ErrUnreachable = errors.New("subsystem unreachable")

// StorageJobRequest is asrs-control's StorageJobRequest (Kind STORE or RETRIEVE; PortID empty for a retrieve).
type StorageJobRequest struct {
	Kind        string
	ContainerID string
	PortID      string
	Reference   string
}

// LegRequest is amr-fleet-control's leg request: From and To are handoff point ids.
type LegRequest struct {
	ContainerID string
	From        string
	To          string
	Reference   string
}

// Accepted is a 2xx answer: the subsystem's id of the job or leg and its status.
type Accepted struct {
	ID     string
	Status string
}

// Refusal is a problem+json answer with its stable code and the replay class the subsystem documents for it.
type Refusal struct {
	Status    int
	Code      string
	Detail    string
	Retryable bool
}

func (r *Refusal) Error() string {
	return fmt.Sprintf("subsystem refused %d %s: %s", r.Status, r.Code, r.Detail)
}

// NotCancellable reports the refusals that end a cancellation attempt (asrs-control and amr-fleet-control).
func NotCancellable(code string) bool {
	return code == "job-not-cancellable" || code == "job-ended" || code == "leg-not-cancellable" || code == "leg-ended"
}
