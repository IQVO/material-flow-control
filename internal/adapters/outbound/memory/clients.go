package memory

import (
	"context"
	"fmt"
	"sync"

	"github.com/IQVO/material-flow-control/internal/application/subsystem"
)

// Call is one recorded subsystem call.
type Call struct {
	Op          string
	Key         string
	SubsystemID string
	Storage     subsystem.StorageJobRequest
	Leg         subsystem.LegRequest
	TargetID    string
	PortID      string
	ContainerID string
}

type script struct {
	mu      sync.Mutex
	calls   []Call
	answers []error
	n       int
}

func (s *script) record(c Call) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, c)
	if len(s.answers) == 0 {
		s.n++
		return nil
	}
	err := s.answers[0]
	s.answers = s.answers[1:]
	if err == nil {
		s.n++
	}
	return err
}

// Answer queues the answers of the next calls (nil = success); unqueued calls succeed.
func (s *script) Answer(errs ...error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.answers = append(s.answers, errs...)
}

// Calls returns every call in order.
func (s *script) Calls() []Call {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Call(nil), s.calls...)
}

// observe records a read call: it consumes no queued answer and does not advance the id counter.
// carry (Task 8 review): RecommendedInputPort used record, which ate the next call's answer and bumped sj-N.
func (s *script) observe(c Call) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, c)
}

func (s *script) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.n
}

// Asrs fakes asrs-control: successful jobs are sj-1, sj-2, …; Recommended is the recommended input port.
type Asrs struct {
	script
	Recommended  string
	RecommendErr error
}

func NewAsrs() *Asrs { return &Asrs{} }

func (a *Asrs) CreateStorageJob(_ context.Context, key, asrsID string, r subsystem.StorageJobRequest) (subsystem.Accepted, error) {
	if err := a.record(Call{Op: "CreateStorageJob", Key: key, SubsystemID: asrsID, Storage: r}); err != nil {
		return subsystem.Accepted{}, err
	}
	return subsystem.Accepted{ID: fmt.Sprintf("sj-%d", a.count()), Status: "QUEUED"}, nil
}

func (a *Asrs) CancelStorageJob(_ context.Context, key, asrsID, jobID string) (subsystem.Accepted, error) {
	if err := a.record(Call{Op: "CancelStorageJob", Key: key, SubsystemID: asrsID, TargetID: jobID}); err != nil {
		return subsystem.Accepted{}, err
	}
	return subsystem.Accepted{ID: jobID, Status: "CANCEL_REQUESTED"}, nil
}

func (a *Asrs) ConfirmPickup(_ context.Context, key, asrsID, portID, containerID string) error {
	return a.record(Call{Op: "ConfirmPickup", Key: key, SubsystemID: asrsID, PortID: portID, ContainerID: containerID})
}

// SetRecommended sets what RecommendedInputPort answers, under the script's mutex (a test may change it while the
// dispatcher runs). carry (Task 8 review)
func (a *Asrs) SetRecommended(port string, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.Recommended, a.RecommendErr = port, err
}

func (a *Asrs) RecommendedInputPort(_ context.Context, asrsID string) (string, error) {
	a.observe(Call{Op: "RecommendedInputPort", SubsystemID: asrsID})
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.Recommended, a.RecommendErr
}

// Amr fakes amr-fleet-control: successful legs are leg-1, leg-2, …
type Amr struct{ script }

func NewAmr() *Amr { return &Amr{} }

func (a *Amr) RequestLeg(_ context.Context, key, fleetID string, r subsystem.LegRequest) (subsystem.Accepted, error) {
	if err := a.record(Call{Op: "RequestLeg", Key: key, SubsystemID: fleetID, Leg: r}); err != nil {
		return subsystem.Accepted{}, err
	}
	return subsystem.Accepted{ID: fmt.Sprintf("leg-%d", a.count()), Status: "QUEUED"}, nil
}

func (a *Amr) CancelLeg(_ context.Context, key, fleetID, legID string) (subsystem.Accepted, error) {
	if err := a.record(Call{Op: "CancelLeg", Key: key, SubsystemID: fleetID, TargetID: legID}); err != nil {
		return subsystem.Accepted{}, err
	}
	return subsystem.Accepted{ID: legID, Status: "CANCEL_REQUESTED"}, nil
}
