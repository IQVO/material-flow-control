package events_test

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/IQVO/material-flow-control/internal/adapters/outbound/events"
	"github.com/IQVO/material-flow-control/internal/application/outbox"
	"github.com/IQVO/material-flow-control/internal/domain/capacity"
	ct "github.com/IQVO/material-flow-control/internal/domain/containertrack"
	"github.com/IQVO/material-flow-control/internal/domain/transportorder"
	tr "github.com/IQVO/material-flow-control/internal/domain/transportrequest"
)

var (
	t0  = time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)
	due = time.Date(2026, 11, 20, 9, 30, 0, 0, time.UTC)
)

type rows struct{ msgs []outbox.Message }

func (r *rows) Insert(_ context.Context, msgs ...outbox.Message) error {
	r.msgs = append(r.msgs, msgs...)
	return nil
}

func ids() func() string {
	n := 0
	return func() string {
		n++
		return "00000000-0000-4000-8000-00000000000" + string(rune('0'+n))
	}
}

func data(t *testing.T, m outbox.Message) map[string]any {
	t.Helper()
	var env struct {
		SpecVersion string         `json:"specversion"`
		Source      string         `json:"source"`
		Type        string         `json:"type"`
		Subject     string         `json:"subject"`
		Data        map[string]any `json:"data"`
	}
	if err := json.Unmarshal(m.Value, &env); err != nil {
		t.Fatal(err)
	}
	if env.SpecVersion != "1.0" || env.Source != "/warehouse/material-flow-control" || env.Type != m.EventType || env.Subject != m.Subject {
		t.Fatalf("envelope = %+v (row type %s subject %s)", env, m.EventType, m.Subject)
	}
	return env.Data
}

func want(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestEverySevenFactIsEncodedOnBothTopicsWithItsExactPayload(t *testing.T) {
	cases := []struct {
		ev      any
		typ     string
		subject string
		data    string
	}{
		{tr.TransportRejected{RequestID: "task-1.1", ContainerID: "TOTE-1", Source: "WH1-AUTO-ASRS-ASRS1-00-00-S", Destination: "ST1",
			Purpose: tr.PurposePick, Reason: tr.ReasonContainerBusy, Detail: "the container has an active transport order", At: t0},
			"com.warehouse.wes.material-flow-control.transportrequest.TransportRejected", "task-1.1",
			`{"transport_request_id":"task-1.1","container_id":"TOTE-1","source":"WH1-AUTO-ASRS-ASRS1-00-00-S","destination":"ST1",
			  "purpose":"PICK","reason":"CONTAINER_BUSY","detail":"the container has an active transport order","rejected_at":"2026-11-20T08:00:00Z"}`},
		{transportorder.TransportAccepted{OrderID: "to-1", RequestID: "task-1.1", ContainerID: "TOTE-1", Source: "WH1-AUTO-ASRS-ASRS1-00-00-S",
			Destination: "ST1", Purpose: tr.PurposePick, Priority: tr.PriorityUrgent, DueBy: due, Legs: 2, At: t0},
			"com.warehouse.wes.material-flow-control.transportorder.TransportAccepted", "to-1",
			`{"transport_order_id":"to-1","transport_request_id":"task-1.1","container_id":"TOTE-1","source":"WH1-AUTO-ASRS-ASRS1-00-00-S",
			  "destination":"ST1","purpose":"PICK","priority":"URGENT","due_by":"2026-11-20T09:30:00Z","legs_planned":2,"accepted_at":"2026-11-20T08:00:00Z"}`},
		{transportorder.ContainerArrivedAtStation{OrderID: "to-1", RequestID: "task-1.1", ContainerID: "TOTE-1", StationID: "ST1",
			Purpose: tr.PurposePick, Priority: tr.PriorityUrgent, DueBy: due, QueuePosition: 2, At: t0},
			"com.warehouse.wes.material-flow-control.transportorder.ContainerArrivedAtStation", "to-1",
			`{"transport_order_id":"to-1","transport_request_id":"task-1.1","container_id":"TOTE-1","station_id":"ST1","purpose":"PICK",
			  "priority":"URGENT","due_by":"2026-11-20T09:30:00Z","queue_position":2,"arrived_at":"2026-11-20T08:00:00Z"}`},
		{transportorder.TransportCompleted{OrderID: "to-1", RequestID: "task-1.1", ContainerID: "TOTE-1", Destination: "ST1", Purpose: tr.PurposePick, At: t0},
			"com.warehouse.wes.material-flow-control.transportorder.TransportCompleted", "to-1",
			`{"transport_order_id":"to-1","transport_request_id":"task-1.1","container_id":"TOTE-1","destination":"ST1","purpose":"PICK",
			  "completed_at":"2026-11-20T08:00:00Z"}`},
		{transportorder.TransportFailed{OrderID: "to-2", RequestID: "task-2.1", ContainerID: "TOTE-2", Purpose: tr.PurposePutaway,
			Reason: transportorder.FailSubsystemFault, SubsystemReason: "ROBOT_BLOCKED", ContainerLocation: "WH1-RCV-DEC1", At: t0},
			"com.warehouse.wes.material-flow-control.transportorder.TransportFailed", "to-2",
			`{"transport_order_id":"to-2","transport_request_id":"task-2.1","container_id":"TOTE-2","purpose":"PUTAWAY","reason":"SUBSYSTEM_FAULT",
			  "subsystem_reason":"ROBOT_BLOCKED","container_location":"WH1-RCV-DEC1","failed_at":"2026-11-20T08:00:00Z"}`},
		{transportorder.TransportFailed{OrderID: "to-3", RequestID: "task-3.1", ContainerID: "TOTE-3", Purpose: tr.PurposePick,
			Reason: transportorder.FailTimeout, At: t0},
			"com.warehouse.wes.material-flow-control.transportorder.TransportFailed", "to-3",
			`{"transport_order_id":"to-3","transport_request_id":"task-3.1","container_id":"TOTE-3","purpose":"PICK","reason":"TIMEOUT",
			  "failed_at":"2026-11-20T08:00:00Z"}`},
		{ct.ContainerSentToClarification{ContainerID: "TOTE-4", OrderID: "to-4", RequestID: "task-4.1", Reason: ct.ReasonMismatch,
			ScannedContainerID: "TOTE-9", DetectedAt: "WH1-AUTO-ASRS-ASRS1-01-00-OUT1", ClarificationPoint: "WH1-CLR-CLR1", At: t0},
			"com.warehouse.wes.material-flow-control.containertrack.ContainerSentToClarification", "TOTE-4",
			`{"container_id":"TOTE-4","transport_order_id":"to-4","transport_request_id":"task-4.1","reason":"MISMATCH","scanned_container_id":"TOTE-9",
			  "detected_at_location":"WH1-AUTO-ASRS-ASRS1-01-00-OUT1","clarification_point":"WH1-CLR-CLR1","sent_at":"2026-11-20T08:00:00Z"}`},
		{capacity.SubsystemCapacityChanged{Snapshot: capacity.Fleet("AMR1", 0, 0, 0, 2, 1, 30, t0)},
			"com.warehouse.wes.material-flow-control.subsystemcapacity.SubsystemCapacityChanged", "AMR_FLEET/AMR1",
			`{"subsystem_kind":"AMR_FLEET","subsystem_id":"AMR1","status":"UNAVAILABLE","units_total":0,"units_available":0,"open_work":3,
			  "capacity_per_hour":0,"buffer_limit":0,"occupied":0,"queue_depth":0,"measured_at":"2026-11-20T08:00:00Z"}`},
		{capacity.SubsystemCapacityChanged{Snapshot: capacity.Station("ST1", 2, 2, 1, t0)},
			"com.warehouse.wes.material-flow-control.subsystemcapacity.SubsystemCapacityChanged", "STATION/ST1",
			`{"subsystem_kind":"STATION","subsystem_id":"ST1","status":"DEGRADED","units_total":2,"units_available":0,"open_work":3,
			  "capacity_per_hour":0,"buffer_limit":2,"occupied":2,"queue_depth":1,"measured_at":"2026-11-20T08:00:00Z"}`},
	}
	for _, c := range cases {
		out := &rows{}
		if err := events.NewSink(out, ids()).Append(context.Background(), c.ev); err != nil {
			t.Fatalf("%T: %v", c.ev, err)
		}
		if len(out.msgs) != 2 || out.msgs[0].Topic != events.TopicEvents || out.msgs[1].Topic != events.TopicAnalytics ||
			out.msgs[0].EventID != out.msgs[1].EventID {
			t.Fatalf("%T: rows = %+v", c.ev, out.msgs)
		}
		name := c.typ[strings.LastIndex(c.typ, ".")+1:]
		for i, stream := range []string{"events", "analytics"} {
			m := out.msgs[i]
			if m.EventType != c.typ || m.Subject != c.subject || string(m.Key) != c.subject ||
				m.DataSchema != "urn:warehouse:material-flow-control:"+stream+":"+name+":v1" {
				t.Fatalf("%T on %s: type %s subject %s key %s schema %s", c.ev, stream, m.EventType, m.Subject, m.Key, m.DataSchema)
			}
			if got := data(t, m); !reflect.DeepEqual(got, want(t, c.data)) {
				t.Fatalf("%T on %s: data =\n%v\nwant\n%v", c.ev, stream, got, want(t, c.data))
			}
		}
	}
}

func TestNoEquipmentVocabularyReachesTheWes(t *testing.T) {
	out := &rows{}
	evs := []any{
		transportorder.TransportAccepted{OrderID: "to-1", RequestID: "r", ContainerID: "C", Source: "A", Destination: "B", At: t0, DueBy: due},
		transportorder.TransportFailed{OrderID: "to-1", RequestID: "r", ContainerID: "C", Reason: transportorder.FailSubsystemFault, At: t0},
		ct.ContainerSentToClarification{ContainerID: "C", Reason: ct.ReasonOnFailedRobot, DetectedAt: "AMR1", At: t0},
	}
	if err := events.NewSink(out, ids()).Append(context.Background(), evs...); err != nil {
		t.Fatal(err)
	}
	for _, m := range out.msgs {
		for k := range data(t, m) {
			for _, word := range []string{"robot", "leg", "node", "shuttle", "aisle", "rack", "storage_job", "port"} {
				// Deviation (Task 17): match whole snake_case words, not substrings: "port" is inside every
				// "transport_*" field and "leg" inside the contract's "legs_planned" (a count, no leg identity).
				if strings.Contains("_"+k+"_", "_"+word+"_") {
					t.Fatalf("%s carries the equipment field %q (seam decision 3 rules)", m.EventType, k)
				}
			}
		}
	}
}

func TestAnUnknownEventWritesNothing(t *testing.T) {
	out := &rows{}
	err := events.NewSink(out, ids()).Append(context.Background(),
		transportorder.TransportCompleted{OrderID: "to-1", At: t0}, struct{ X int }{1})
	if err == nil || !strings.Contains(err.Error(), "no CloudEvents encoder") || len(out.msgs) != 0 {
		t.Fatalf("err = %v, rows %d", err, len(out.msgs))
	}
}
