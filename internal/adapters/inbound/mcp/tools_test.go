package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/IQVO/material-flow-control/internal/application/facts"
	"github.com/IQVO/material-flow-control/internal/application/usecases"
	"github.com/IQVO/material-flow-control/internal/domain/capacity"
	ct "github.com/IQVO/material-flow-control/internal/domain/containertrack"
	"github.com/IQVO/material-flow-control/internal/domain/layout"
	"github.com/IQVO/material-flow-control/internal/domain/stationqueue"
	"github.com/IQVO/material-flow-control/internal/domain/transportorder"
	tr "github.com/IQVO/material-flow-control/internal/domain/transportrequest"
)

var t0 = time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)

type fakeReads struct{}

func (fakeReads) Transport(_ context.Context, id string) (usecases.TransportView, error) {
	if id != "task-1.1" {
		return usecases.TransportView{}, usecases.ErrRequestNotFound
	}
	return usecases.TransportView{RequestID: id, OrderID: "to-1", ContainerID: "TOTE-1", Purpose: tr.PurposePick,
		Status: usecases.ViewInProgress, LegsTotal: 2, LegsDone: 1, AcceptedAt: t0}, nil
}

func (fakeReads) OrderDetail(_ context.Context, id string) (transportorder.Snapshot, error) {
	return transportorder.Snapshot{ID: "to-1", RequestID: id, Legs: []transportorder.Leg{
		{Seq: 1, Attempt: 1, Ref: "to-1.1.1", Subsystem: "ASRS", SubsystemID: "ASRS1", Kind: "RETRIEVE", Status: transportorder.LegDone, SubsystemRef: "sj-7"},
		{Seq: 2, Attempt: 1, Ref: "to-1.2.1", Subsystem: "AMR_FLEET", SubsystemID: "AMR1", Kind: "MOVE", Status: transportorder.LegPickedUp,
			SubsystemRef: "leg-3", Robot: "EMU/R1", From: layout.Place{Code: "WH1-AUTO-ASRS-ASRS1-01-00-OUT1"}, To: layout.Place{Code: "ST1"}},
	}}, nil
}

func (fakeReads) Track(_ context.Context, id string) (usecases.TrackView, error) {
	return usecases.TrackView{Track: ct.Snapshot{ContainerID: id, Position: ct.OnRobotOf("AMR1", "EMU/R1"), UpdatedAt: t0}}, nil
}

func (fakeReads) Station(_ context.Context, id string) (stationqueue.Snapshot, error) {
	if id != "ST1" {
		return stationqueue.Snapshot{}, usecases.ErrStationNotFound
	}
	return stationqueue.Snapshot{StationID: id, BufferLimit: 2}, nil
}

func (fakeReads) Subsystems(context.Context) ([]capacity.Snapshot, error) {
	return []capacity.Snapshot{capacity.Fleet("AMR1", 3, 3, 2, 1, 1, 30, t0)}, nil
}

func (fakeReads) EquipmentUnits(_ context.Context, asrsID string) ([]facts.EquipmentState, error) {
	return []facts.EquipmentState{{AsrsID: asrsID, UnitID: "A1", Aisle: 1, State: "HELD", PreviousState: "IDLE", At: t0}}, nil
}

func call(t *testing.T, name string, args map[string]any) (map[string]any, bool) {
	t.Helper()
	res, err := session(t, Deps{Reads: fakeReads{}}).CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	b, _ := json.Marshal(res.StructuredContent)
	_ = json.Unmarshal(b, &out)
	return out, res.IsError
}

func TestTheToolsAnswerWithEquipmentDetailOnlyHere(t *testing.T) {
	out, isErr := call(t, "get_transport", map[string]any{"transportRequestId": "task-1.1"})
	legs, _ := out["legs"].([]any)
	if isErr || out["status"] != "IN_PROGRESS" || len(legs) != 2 || legs[1].(map[string]any)["robot"] != "EMU/R1" ||
		legs[1].(map[string]any)["subsystem_ref"] != "leg-3" {
		t.Fatalf("get_transport = %v %v", out, isErr)
	}
	out, _ = call(t, "get_container_track", map[string]any{"containerId": "TOTE-1"})
	pos, _ := out["position"].(map[string]any)
	if pos["kind"] != "ON_ROBOT" || pos["code"] != "AMR1" || pos["robot"] != "EMU/R1" {
		t.Fatalf("get_container_track = %v", out)
	}
	out, _ = call(t, "list_subsystems", map[string]any{})
	if subs, _ := out["subsystems"].([]any); len(subs) != 1 || subs[0].(map[string]any)["open_work"] != float64(2) {
		t.Fatalf("list_subsystems = %v", out)
	}
	out, _ = call(t, "list_equipment_units", map[string]any{"asrsId": "ASRS1"})
	if units, _ := out["units"].([]any); len(units) != 1 || units[0].(map[string]any)["state"] != "HELD" {
		t.Fatalf("list_equipment_units = %v", out)
	}
}

func TestAnUnknownIdIsAToolErrorWithItsSlug(t *testing.T) {
	res, err := session(t, Deps{Reads: fakeReads{}}).CallTool(context.Background(),
		&mcp.CallToolParams{Name: "get_station", Arguments: map[string]any{"stationId": "ST9"}})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(res.Content)
	if !res.IsError || !strings.Contains(string(b), "station-not-found") {
		t.Fatalf("unknown station = %+v", res)
	}
}
