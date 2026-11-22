package amr_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/IQVO/material-flow-control/internal/adapters/outbound/amr"
	"github.com/IQVO/material-flow-control/internal/application/subsystem"
)

// carry (Task 8 review): plan 03's relationship-4 table classes leg-not-cancellable and leg-ended 409 R
// (RETRYABLE); the real client must agree with the memory fake.
func assertACancelRefusalIsRetryable(t *testing.T, code string) {
	t.Helper()
	var path, key string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, key = r.URL.Path, r.Header.Get("Idempotency-Key")
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w, `{"status":409,"code":"`+code+`"}`)
	}))
	t.Cleanup(srv.Close)
	_, err := amr.New(srv.URL, time.Second).CancelLeg(context.Background(), "cancel-to-1.2.1", "AMR1", "leg-1")
	var r *subsystem.Refusal
	if !errors.As(err, &r) || r.Status != http.StatusConflict || r.Code != code || !r.Retryable {
		t.Fatalf("%s = %v", code, err)
	}
	if path != "/fleets/AMR1/legs/leg-1/cancellation" || key != "cancel-to-1.2.1" {
		t.Fatalf("cancel call = %s %s", path, key)
	}
}

// carry (Task 8 review)
func TestLegNotCancellableIsRetryable(t *testing.T) {
	assertACancelRefusalIsRetryable(t, "leg-not-cancellable")
}

// carry (Task 8 review)
func TestLegEndedIsRetryable(t *testing.T) { assertACancelRefusalIsRetryable(t, "leg-ended") }

// carry (Task 14 review): the relationship-4 table answers a Leg (leg_id) on the 202 of RequestLeg and on the
// 200/202 of CancelLeg; a 2xx without it is not an acceptance the dispatcher can track.
func TestALegAnswerWithoutAnIDIsUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{}`)
	}))
	t.Cleanup(srv.Close)
	c := amr.New(srv.URL, time.Second)
	_, err := c.RequestLeg(context.Background(), "k", "AMR1", subsystem.LegRequest{ContainerID: "C", From: "A", To: "B", Reference: "r"})
	if !errors.Is(err, subsystem.ErrUnreachable) || !strings.Contains(err.Error(), "/fleets/AMR1/legs answered no id") {
		t.Fatalf("request = %v", err)
	}
	_, err = c.CancelLeg(context.Background(), "k", "AMR1", "leg-1")
	if !errors.Is(err, subsystem.ErrUnreachable) || !strings.Contains(err.Error(), "/fleets/AMR1/legs/leg-1/cancellation answered no id") {
		t.Fatalf("cancel = %v", err)
	}
}

// carry (Task 19 review, plan 03 P3-R14): `reference` is optional and amr-fleet-control refuses reference ""
// (Id minLength 1, bad-request 400 FINAL), so an empty reference is omitted from the LegRequest body.
func TestAnEmptyReferenceIsOmittedFromTheLegRequest(t *testing.T) {
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		bodies = append(bodies, b)
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"leg_id":"leg-1","status":"REQUESTED"}`)
	}))
	t.Cleanup(srv.Close)
	c := amr.New(srv.URL, time.Second)
	for _, ref := range []string{"", "to-1.2.1"} {
		if _, err := c.RequestLeg(context.Background(), "k-"+ref, "AMR1",
			subsystem.LegRequest{ContainerID: "TOTE-1", From: "ASRS1-OUT1", To: "ST1", Reference: ref}); err != nil {
			t.Fatal(err)
		}
	}
	if _, has := bodies[0]["reference"]; has || len(bodies[0]) != 3 || bodies[0]["container_id"] != "TOTE-1" {
		t.Fatalf("empty reference body = %v, want no reference key", bodies[0])
	}
	if bodies[1]["reference"] != "to-1.2.1" || len(bodies[1]) != 4 {
		t.Fatalf("reference body = %v", bodies[1])
	}
}

// carry (Task 19 review, plan 03 P3-R13): a refusal decided by configuration that can change inside the 24 h
// replay window (the map file: unknown-handoff-point, fleet-not-found; robots are created by robot facts:
// robot-not-found) is RETRYABLE, so a map fix deploy does not leave the dispatcher's same-key retries pinned.
func TestTheConfigurationDecidedRefusalsAreRetryable(t *testing.T) {
	for code, status := range map[string]int{"unknown-handoff-point": http.StatusUnprocessableEntity,
		"fleet-not-found": http.StatusNotFound, "robot-not-found": http.StatusNotFound} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(status)
			_, _ = io.WriteString(w, `{"status":`+strconv.Itoa(status)+`,"code":"`+code+`"}`)
		}))
		_, err := amr.New(srv.URL, time.Second).RequestLeg(context.Background(), "k", "AMR1",
			subsystem.LegRequest{ContainerID: "C", From: "A", To: "B", Reference: "r"})
		srv.Close()
		var r *subsystem.Refusal
		if !errors.As(err, &r) || r.Status != status || r.Code != code || !r.Retryable {
			t.Errorf("%s = %v, want a RETRYABLE %d refusal", code, err, status)
		}
	}
}
