package transportrequest_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	tr "github.com/IQVO/material-flow-control/internal/domain/transportrequest"
)

var t0 = time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)

func valid() tr.Request {
	return tr.Request{ID: "task-17-1", ContainerID: "TOTE-1", Source: "WH1-AUTO-ASRS-ASRS1-00-00-S", Destination: "ST1",
		Purpose: tr.PurposePick, Priority: tr.PriorityUrgent, DueBy: t0.Add(time.Hour)}
}

func TestAValidRequestPassesAndEveryRuleIsChecked(t *testing.T) {
	if err := valid().Validate(); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		mutate func(r *tr.Request)
		want   string
	}{
		{"id", func(r *tr.Request) { r.ID = "" }, "transport_request_id"},
		{"long id", func(r *tr.Request) { r.ID = strings.Repeat("a", 129) }, "transport_request_id"},
		{"control char", func(r *tr.Request) { r.Source = "A\x00B" }, "source"},
		{"container", func(r *tr.Request) { r.ContainerID = "TOTE 1" }, "container_id"},
		{"long container", func(r *tr.Request) { r.ContainerID = strings.Repeat("c", 65) }, "container_id"},
		{"destination", func(r *tr.Request) { r.Destination = "" }, "destination"},
		{"purpose", func(r *tr.Request) { r.Purpose = "SORT" }, "purpose"},
		{"priority", func(r *tr.Request) { r.Priority = "HIGH" }, "priority"},
		{"due", func(r *tr.Request) { r.DueBy = time.Time{} }, "due_by"},
	}
	for _, c := range cases {
		r := valid()
		c.mutate(&r)
		if err := r.Validate(); !errors.Is(err, tr.ErrInvalid) || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s: Validate = %v, want ErrInvalid naming %q", c.name, err, c.want)
		}
	}
	r := valid()
	r.ContainerID = strings.Repeat("c", 64)
	if err := r.Validate(); err != nil {
		t.Fatalf("a 64-character LPN is valid: %v", err)
	}
	r.ID = strings.Repeat("a", 128)
	if err := r.Validate(); err != nil {
		t.Fatalf("a 128-character id is valid: %v", err)
	}
}

func TestTheFingerprintCoversEveryFieldAndNormalizationMakesItStable(t *testing.T) {
	a := valid()
	b := a
	b.DueBy = a.DueBy.In(time.FixedZone("BRT", -3*3600)).Add(400 * time.Nanosecond)
	if a.Normalized().Fingerprint() != b.Normalized().Fingerprint() {
		t.Fatal("the same instant in another zone with sub-microsecond noise must fingerprint alike")
	}
	if !a.Normalized().DueBy.Equal(a.DueBy) || a.Normalized().DueBy.Location() != time.UTC {
		t.Fatal("Normalized keeps the instant in UTC")
	}
	for _, mutate := range []func(r *tr.Request){
		func(r *tr.Request) { r.ContainerID = "TOTE-2" },
		func(r *tr.Request) { r.Source = "ST1" },
		func(r *tr.Request) { r.Destination = "ST2" },
		func(r *tr.Request) { r.Purpose = tr.PurposeReplenish },
		func(r *tr.Request) { r.Priority = tr.PriorityNormal },
		func(r *tr.Request) { r.DueBy = r.DueBy.Add(time.Second) },
	} {
		c := valid()
		mutate(&c)
		if c.Fingerprint() == a.Fingerprint() {
			t.Fatalf("a changed field kept the fingerprint: %+v", c)
		}
	}
}

func TestARecordKeepsTheOneOutcome(t *testing.T) {
	acc := tr.Accept(valid(), "to-1", t0)
	if acc.Outcome != tr.OutcomeAccepted || acc.OrderID != "to-1" || acc.Reason != "" || !acc.At.Equal(t0) || !acc.SameRequest(valid()) {
		t.Fatalf("accepted = %+v", acc)
	}
	other := valid()
	other.Destination = "ST2"
	if acc.SameRequest(other) {
		t.Fatal("another request matched")
	}
	rec, ev := tr.Reject(valid(), tr.ReasonContainerBusy, "the container has an active transport order", t0)
	if rec.Outcome != tr.OutcomeRejected || rec.OrderID != "" || rec.Reason != tr.ReasonContainerBusy {
		t.Fatalf("rejected = %+v", rec)
	}
	want := tr.TransportRejected{RequestID: "task-17-1", ContainerID: "TOTE-1", Source: "WH1-AUTO-ASRS-ASRS1-00-00-S", Destination: "ST1",
		Purpose: tr.PurposePick, Reason: tr.ReasonContainerBusy, Detail: "the container has an active transport order", At: t0}
	if ev != want {
		t.Fatalf("event = %+v", ev)
	}
	if tr.PriorityUrgent.Rank() != 0 || tr.PriorityNormal.Rank() != 1 {
		t.Fatal("URGENT ranks before NORMAL")
	}
}
