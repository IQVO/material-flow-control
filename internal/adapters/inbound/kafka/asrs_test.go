package kafka

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/IQVO/material-flow-control/internal/application/facts"
)

var at = time.Date(2026, 11, 20, 8, 0, 1, 0, time.UTC)

func TestEveryAsrsFactDecodesToItsExactValue(t *testing.T) {
	cases := []struct {
		typ  string
		data map[string]any
		want any
	}{
		{TypeContainerStored, map[string]any{"storage_job_id": "sj-1", "asrs_id": "ASRS1", "container_id": "C-1", "port_id": "IN1", "reference": "to-1.2.1",
			"stored_at": "2026-11-20T08:00:01Z", "plc_timestamp": "2026-11-20T09:00:00Z"},
			facts.ContainerStored{JobID: "sj-1", AsrsID: "ASRS1", ContainerID: "C-1", PortID: "IN1", Reference: "to-1.2.1", At: at}},
		{TypeContainerPresented, map[string]any{"storage_job_id": "sj-2", "asrs_id": "ASRS1", "container_id": "C-1", "port_id": "OUT1", "reference": "to-1.1.1",
			"scan_result": "MISMATCH", "scanned_container_id": "C-9", "presented_at": "2026-11-20T08:00:01Z"},
			facts.ContainerPresented{JobID: "sj-2", AsrsID: "ASRS1", ContainerID: "C-1", PortID: "OUT1", Reference: "to-1.1.1", ScanResult: "MISMATCH", ScannedContainerID: "C-9", At: at}},
		{TypeStorageJobFailed, map[string]any{"storage_job_id": "sj-3", "asrs_id": "ASRS1", "job_kind": "STORE", "container_id": "C-1", "port_id": "IN1",
			"reference": "r", "reason": "EQUIPMENT_FAULT", "plc_reason": "E17", "failed_at": "2026-11-20T08:00:01Z"},
			facts.StorageJobFailed{JobID: "sj-3", AsrsID: "ASRS1", Kind: "STORE", ContainerID: "C-1", PortID: "IN1", Reference: "r", Reason: "EQUIPMENT_FAULT", PLCReason: "E17", At: at}},
		{TypeEquipmentStateChanged, map[string]any{"asrs_id": "ASRS1", "unit_id": "A1", "aisle": 1, "state": "HELD", "previous_state": "IDLE", "changed_at": "2026-11-20T08:00:01Z"},
			facts.EquipmentState{AsrsID: "ASRS1", UnitID: "A1", Aisle: 1, State: "HELD", PreviousState: "IDLE", At: at}},
		{TypeAsrsCapacityChanged, map[string]any{"asrs_id": "ASRS1", "units_total": 2, "units_available": 1, "positions_total": 64, "positions_free": 60,
			"open_jobs": 3, "measured_at": "2026-11-20T08:00:01Z"},
			facts.AsrsCapacity{AsrsID: "ASRS1", UnitsTotal: 2, UnitsAvailable: 1, PositionsTotal: 64, PositionsFree: 60, OpenJobs: 3, At: at}},
	}
	for _, tc := range cases {
		r := &recorder{}
		c := testConsumer(AsrsHandlers(r))
		if err := c.HandleMessage(context.Background(), event("id-1", tc.typ, tc.data)); err != nil {
			t.Fatalf("%s: %v", tc.typ, err)
		}
		if len(r.calls) != 1 || !reflect.DeepEqual(r.calls[0], tc.want) || r.ids[0] != "id-1" {
			t.Fatalf("%s: calls = %+v, want %+v", tc.typ, r.calls, tc.want)
		}
	}
}

func TestAMalformedAsrsFactIsPoisonAndNeverReachesTheUseCase(t *testing.T) {
	bad := []struct {
		typ  string
		data map[string]any
	}{
		{TypeContainerStored, map[string]any{"storage_job_id": "sj-1", "asrs_id": "ASRS1", "port_id": "IN1", "stored_at": "2026-11-20T08:00:01Z"}},
		{TypeContainerPresented, map[string]any{"storage_job_id": "sj-1", "asrs_id": "ASRS1", "container_id": "C", "port_id": "OUT1", "scan_result": "MAYBE", "presented_at": "2026-11-20T08:00:01Z"}},
		{TypeStorageJobFailed, map[string]any{"storage_job_id": "sj-1", "asrs_id": "ASRS1", "job_kind": "STORE", "container_id": "C", "port_id": "IN1", "failed_at": "2026-11-20T08:00:01Z"}},
		{TypeEquipmentStateChanged, map[string]any{"asrs_id": "ASRS1", "unit_id": "A1", "aisle": "one", "state": "HELD"}},
		{TypeAsrsCapacityChanged, map[string]any{"units_total": 2, "measured_at": "2026-11-20T08:00:01Z"}},
		{TypeContainerStored, map[string]any{"storage_job_id": "sj-1", "asrs_id": "ASRS1", "container_id": "C", "port_id": "IN1", "stored_at": "yesterday"}},
	}
	for _, tc := range bad {
		r := &recorder{}
		err := testConsumer(AsrsHandlers(r)).HandleMessage(context.Background(), event("id", tc.typ, tc.data))
		if !errors.Is(err, errPoison) || r.count() != 0 {
			t.Fatalf("%s %v: err = %v, calls %d", tc.typ, tc.data, err, r.count())
		}
	}
}

func TestOtherTypesAndNonCloudEventsAreSkippedAndTransientErrorsReturned(t *testing.T) {
	r := &recorder{}
	c := testConsumer(AsrsHandlers(r))
	if err := c.HandleMessage(context.Background(), event("id", "com.warehouse.wes.asrs-control.storagejob.SomethingNew", map[string]any{})); err != nil {
		t.Fatalf("unknown type = %v", err)
	}
	if err := c.HandleMessage(context.Background(), []byte(`{"not":"a cloudevent"}`)); err != nil {
		t.Fatalf("not a CloudEvent = %v", err)
	}
	r.failures = 1
	err := c.HandleMessage(context.Background(), event("id", TypeAsrsCapacityChanged, map[string]any{"asrs_id": "ASRS1", "units_total": 1,
		"units_available": 1, "positions_total": 1, "positions_free": 1, "open_jobs": 0, "measured_at": "2026-11-20T08:00:01Z"}))
	if !errors.Is(err, errTransient) || errors.Is(err, errPoison) || r.count() != 0 {
		t.Fatalf("transient = %v", err)
	}
}
