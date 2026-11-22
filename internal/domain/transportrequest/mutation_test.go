package transportrequest_test

import (
	"errors"
	"strings"
	"testing"

	tr "github.com/IQVO/material-flow-control/internal/domain/transportrequest"
)

// carry (Task 4 review, Task 26 mutation work): a control character is refused wherever it stands, the FIRST
// position included (kills IndexFunc(...) >= 0 -> > 0).
func TestAControlCharacterIsRefusedAtTheFirstPosition(t *testing.T) {
	for _, c := range []struct {
		name  string
		set   func(r *tr.Request)
		field string
	}{
		{"source", func(r *tr.Request) { r.Source = "\x00AB" }, "source"},
		{"destination", func(r *tr.Request) { r.Destination = "\tST1" }, "destination"},
		{"id", func(r *tr.Request) { r.ID = "\nrq-1" }, "transport_request_id"},
	} {
		r := valid()
		c.set(&r)
		if err := r.Validate(); !errors.Is(err, tr.ErrInvalid) || !strings.Contains(err.Error(), c.field+" must not contain control characters") {
			t.Fatalf("%s: Validate = %v, want the control-character refusal naming %s", c.name, err, c.field)
		}
	}
}

// carry (Task 4 review, Task 26 mutation work): the container id alphabet is exactly ^[A-Za-z0-9._-]{1,64}$: every
// range edge is in, every neighbour of an edge is out, and so is a non-ASCII letter or digit, a first character
// outside the alphabet and the empty id.
func TestTheContainerAlphabetIsExactlyTheLPNRanges(t *testing.T) {
	for _, ok := range []string{"a", "z", "A", "Z", "0", "9", ".", "_", "-", "a.b_c-D9", strings.Repeat("Z", 64)} {
		r := valid()
		r.ContainerID = ok
		if err := r.Validate(); err != nil {
			t.Fatalf("container %q refused: %v", ok, err)
		}
	}
	for _, bad := range []string{"`", "{", "@", "[", "/", ":", "~", "\x7f", " TOTE", "TOTE ", "TÖTE", "١٢", "", strings.Repeat("Z", 65)} {
		r := valid()
		r.ContainerID = bad
		if err := r.Validate(); !errors.Is(err, tr.ErrInvalid) || !strings.Contains(err.Error(), "container_id") {
			t.Fatalf("container %q = %v, want the container_id refusal", bad, err)
		}
	}
}

// carry (Task 4 review): every purpose with every priority is a valid request.
func TestEveryPurposeWithEveryPriorityIsValid(t *testing.T) {
	for _, p := range []tr.Purpose{tr.PurposePick, tr.PurposePutaway, tr.PurposeReplenish, tr.PurposeOutbound, tr.PurposeReturn} {
		for _, pr := range []tr.Priority{tr.PriorityNormal, tr.PriorityUrgent} {
			r := valid()
			r.Purpose, r.Priority = p, pr
			if err := r.Validate(); err != nil {
				t.Fatalf("%s %s: %v", p, pr, err)
			}
		}
	}
}
