package amr_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/IQVO/material-flow-control/internal/adapters/outbound/amr"
	"github.com/IQVO/material-flow-control/internal/application/subsystem"
)

type call struct {
	Path, Key string
	Body      map[string]any
}

// TestALegNamesHandoffPointsAndTheReferenceAndCancellationTheLeg is the plan 04 Task 14 brief's test split for cyclop
// (max 15, package average 10): the leg-body check moved verbatim into assertTheLegCall and the refusal-class loop into
// assertTheRefusalClasses (status and answer now set through set); every assertion is byte-identical to the brief and
// runs in the original order (Task 14 report D1).
func TestALegNamesHandoffPointsAndTheReferenceAndCancellationTheLeg(t *testing.T) {
	var calls []call
	status, answer := http.StatusAccepted, `{"leg_id":"leg-1","fleet_id":"AMR1","status":"QUEUED"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		c := call{Path: r.URL.Path, Key: r.Header.Get("Idempotency-Key")}
		_ = json.Unmarshal(b, &c.Body)
		calls = append(calls, c)
		w.WriteHeader(status)
		_, _ = io.WriteString(w, answer)
	}))
	defer srv.Close()
	c := amr.New(srv.URL, time.Second)
	acc, err := c.RequestLeg(context.Background(), "to-1.2.1", "AMR1", subsystem.LegRequest{ContainerID: "TOTE-1", From: "ASRS1-OUT1", To: "ST1", Reference: "to-1.2.1"})
	if err != nil || acc != (subsystem.Accepted{ID: "leg-1", Status: "QUEUED"}) {
		t.Fatalf("accepted = %+v %v", acc, err)
	}
	assertTheLegCall(t, calls)
	status, answer = http.StatusConflict, `{"status":409,"code":"leg-not-cancellable"}`
	_, err = c.CancelLeg(context.Background(), "cancel-to-1.2.1", "AMR1", "leg-1")
	var r *subsystem.Refusal
	if !errors.As(err, &r) || r.Code != "leg-not-cancellable" || calls[1].Path != "/fleets/AMR1/legs/leg-1/cancellation" || calls[1].Key != "cancel-to-1.2.1" {
		t.Fatalf("cancel = %v %+v", err, calls[1])
	}
	assertTheRefusalClasses(t, c, func(s int, a string) { status, answer = s, a })
}

func assertTheLegCall(t *testing.T, calls []call) {
	t.Helper()
	want := map[string]any{"container_id": "TOTE-1", "from_handoff_point_id": "ASRS1-OUT1", "to_handoff_point_id": "ST1", "reference": "to-1.2.1"}
	if calls[0].Path != "/fleets/AMR1/legs" || calls[0].Key != "to-1.2.1" || len(calls[0].Body) != 4 {
		t.Fatalf("leg call = %+v", calls[0])
	}
	for k, v := range want {
		if calls[0].Body[k] != v {
			t.Fatalf("body[%s] = %v, want %v", k, calls[0].Body[k], v)
		}
	}
}

func assertTheRefusalClasses(t *testing.T, c *amr.Client, set func(status int, answer string)) {
	t.Helper()
	var r *subsystem.Refusal
	for _, code := range []string{"handoff-unreachable", "container-busy", "fleet-unavailable"} {
		set(http.StatusUnprocessableEntity, `{"status":422,"code":"`+code+`"}`)
		_, err := c.RequestLeg(context.Background(), "k", "AMR1", subsystem.LegRequest{ContainerID: "C", From: "A", To: "B", Reference: "r"})
		if !errors.As(err, &r) || !r.Retryable {
			t.Fatalf("%s = %v", code, err)
		}
	}
	set(http.StatusUnprocessableEntity, `{"status":422,"code":"unknown-handoff-point"}`)
	if _, err := c.RequestLeg(context.Background(), "k", "AMR1", subsystem.LegRequest{}); !errors.As(err, &r) || r.Retryable {
		t.Fatalf("unknown-handoff-point = %v", err)
	}
}
