package a2a

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"

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

// fakeReads is Task 20's MCP test double (internal/adapters/inbound/mcp/tools_test.go), copied verbatim.
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

// agent drives the real JSON-RPC wire: card -> SDK client -> handler -> executor -> reads.
func agent(t *testing.T, reads Reads) *a2aclient.Client {
	t.Helper()
	srv := httptest.NewServer(Handler(NewExecutor(Deps{Reads: reads})))
	t.Cleanup(srv.Close)
	client, err := a2aclient.NewFromCard(context.Background(), Card(Config{AgentURL: srv.URL, Version: "test"}))
	if err != nil {
		t.Fatal(err)
	}
	return client
}

// send delivers part and returns the envelope JSON and the text summary of the one reply.
func send(t *testing.T, client *a2aclient.Client, part *a2a.Part) (map[string]any, string) {
	t.Helper()
	resp, err := client.SendMessage(context.Background(), &a2a.SendMessageRequest{Message: a2a.NewMessage(a2a.MessageRoleUser, part)})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	msg, ok := resp.(*a2a.Message)
	if !ok || len(msg.Parts) != 2 {
		t.Fatalf("reply = %T %+v, want one message with a data and a text part", resp, resp)
	}
	raw, _ := json.Marshal(msg.Parts[0].Content.(a2a.Data).Value)
	var env map[string]any
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	return env, string(msg.Parts[1].Content.(a2a.Text))
}

func skill(name string, args map[string]any) *a2a.Part {
	return a2a.NewDataPart(map[string]any{"skill": name, "arguments": args})
}

func problemCode(env map[string]any) string {
	p, _ := env["problem"].(map[string]any)
	s, _ := p["code"].(string)
	return s
}

func TestTheCardOffersExactlyTheFiveReadSkillsAtL1(t *testing.T) {
	card := Card(Config{AgentURL: "http://material-flow-control-a2a:8093/", Version: "v1"})
	if card.Name != "material-flow-control" || card.Capabilities.Streaming || card.SecuritySchemes != nil ||
		len(card.SupportedInterfaces) != 1 || card.SupportedInterfaces[0].URL != "http://material-flow-control-a2a:8093/" ||
		card.SupportedInterfaces[0].ProtocolBinding != a2a.TransportProtocolJSONRPC || card.Version != "v1" || card.Description != cardDescription {
		t.Fatalf("card = %+v", card)
	}
	var ids []string
	for _, s := range card.Skills {
		ids = append(ids, s.ID)
		if !strings.Contains(","+strings.Join(s.Tags, ",")+",", ",authority:L1,") || !strings.Contains(strings.Join(s.Tags, ","), "read") {
			t.Errorf("skill %s tags %v, want read and authority:L1", s.ID, s.Tags)
		}
	}
	sort.Strings(ids)
	if got := strings.Join(ids, ","); got != "container_position,equipment_units,station_queue,subsystem_capacity,transport_status" {
		t.Fatalf("skills = %s", got)
	}
}

func TestTransportStatusAnswersTheLegsWithTheRobot(t *testing.T) {
	env, text := send(t, agent(t, fakeReads{}), skill("transport_status", map[string]any{"transportRequestId": "task-1.1"}))
	data, _ := env["data"].(map[string]any)
	legs, _ := data["legs"].([]any)
	if env["skill"] != "transport_status" || env["problem"] != nil || len(legs) != 2 || legs[1].(map[string]any)["robot"] != "EMU/R1" ||
		legs[1].(map[string]any)["subsystem_ref"] != "leg-3" || data["status"] != "IN_PROGRESS" {
		t.Fatalf("envelope = %v", env)
	}
	if !strings.Contains(text, "IN_PROGRESS") || text != "task-1.1: IN_PROGRESS, leg 2 of 2 (MOVE on AMR1, robot EMU/R1, PICKED_UP)" {
		t.Fatalf("summary = %q", text)
	}
}

func TestAnUnknownStationIsAStationNotFoundProblemWithoutData(t *testing.T) {
	env, text := send(t, agent(t, fakeReads{}), skill("station_queue", map[string]any{"stationId": "ST9"}))
	if problemCode(env) != "station-not-found" || env["data"] != nil || env["skill"] != "station_queue" {
		t.Fatalf("envelope = %v", env)
	}
	p := env["problem"].(map[string]any)
	if p["type"] != "https://errors.material-flow-control.warehouse-systems.dev/station-not-found" || p["status"] != float64(404) ||
		p["detail"] != "station not found" || text != "Station not found (404): station not found" {
		t.Fatalf("problem = %v %q", p, text)
	}
}

func TestAnUnknownSkillIsAnUnknownSkillProblem(t *testing.T) {
	env, _ := send(t, agent(t, fakeReads{}), skill("cancel_transport", map[string]any{"transportRequestId": "task-1.1"}))
	if problemCode(env) != "unknown-skill" || env["data"] != nil {
		t.Fatalf("envelope = %v", env)
	}
}

func TestAnUnknownArgumentIsAnInvalidArgumentProblem(t *testing.T) {
	part := a2a.NewDataPart(map[string]any{"skill": "transport_status", "arguments": map[string]any{"transportRequestId": "x", "extra": 1}})
	env, _ := send(t, agent(t, fakeReads{}), part)
	if problemCode(env) != "invalid-argument" || env["data"] != nil {
		t.Fatalf("envelope = %v", env)
	}
}

func TestTheOtherSkillsAnswerTheMCPPayloadShapes(t *testing.T) {
	client := agent(t, fakeReads{})
	for name, c := range map[string]struct {
		part     *a2a.Part
		data     string
		wantText string
	}{
		"container_position": {skill("container_position", map[string]any{"containerId": "TOTE-1"}),
			`{"container_id":"TOTE-1","moves":0,"position":{"code":"AMR1","kind":"ON_ROBOT","robot":"EMU/R1"},"updated_at":"2026-11-20T08:00:00Z"}`,
			"TOTE-1: ON_ROBOT AMR1 (robot EMU/R1)"},
		"station_queue": {skill("station_queue", map[string]any{"stationId": "ST1"}),
			`{"buffer_limit":2,"occupants":[],"occupied":0,"station_id":"ST1","waiting":[]}`, "ST1: 0/2 occupied, 0 waiting"},
		"subsystem_capacity": {skill("subsystem_capacity", nil),
			`{"subsystems":[{"capacity_per_hour":60,"measured_at":"2026-11-20T08:00:00Z","open_work":2,"queue_depth":0,"status":"DEGRADED",` +
				`"subsystem_id":"AMR1","subsystem_kind":"AMR_FLEET","units_available":2,"units_total":3}]}`,
			"1 subsystem(s): AMR_FLEET/AMR1 DEGRADED 2/3"},
		"equipment_units": {skill("equipment_units", map[string]any{"asrsId": "ASRS1"}),
			`{"asrs_id":"ASRS1","units":[{"aisle":1,"changed_at":"2026-11-20T08:00:00Z","state":"HELD","unit_id":"A1"}]}`,
			"ASRS1: 1 unit(s): A1 HELD"},
	} {
		env, text := send(t, client, c.part)
		raw, _ := json.Marshal(env["data"])
		if env["skill"] != name || string(raw) != c.data || text != c.wantText {
			t.Errorf("%s: data %s summary %q", name, raw, text)
		}
	}
}

func TestRequestShapeProblemsAreAnsweredInBand(t *testing.T) {
	client := agent(t, fakeReads{})
	for name, c := range map[string]struct {
		part *a2a.Part
		want string
	}{
		"missing argument":      {skill("container_position", map[string]any{}), "invalid-argument"},
		"blank argument":        {skill("equipment_units", map[string]any{"asrsId": "  "}), "invalid-argument"},
		"argument on no-args":   {skill("subsystem_capacity", map[string]any{"kind": "ASRS"}), "invalid-argument"},
		"station arguments":     {skill("station_queue", map[string]any{"stationId": 7}), "invalid-argument"},
		"unknown transport":     {skill("transport_status", map[string]any{"transportRequestId": "nope"}), "transport-request-not-found"},
		"no skill":              {a2a.NewDataPart(map[string]any{"arguments": map[string]any{}}), "malformed-skill-request"},
		"not JSON":              {a2a.NewTextPart("where is my container?"), "malformed-skill-request"},
		"unknown request key":   {a2a.NewDataPart(map[string]any{"skill": "station_queue", "args": map[string]any{}}), "malformed-skill-request"},
		"trailing data in text": {a2a.NewTextPart(`{"skill":"station_queue"} {}`), "malformed-skill-request"},
		"a file part":           {a2a.NewFileURLPart("https://x/y", "text/plain"), "malformed-skill-request"},
	} {
		if env, _ := send(t, client, c.part); problemCode(env) != c.want || env["data"] != nil {
			t.Errorf("%s: envelope %v, want only the %s problem", name, env, c.want)
		}
	}
}

func TestATextPartCarriesTheSameRequest(t *testing.T) {
	env, _ := send(t, agent(t, fakeReads{}), a2a.NewTextPart(`{"skill":"station_queue","arguments":{"stationId":"ST1"}}`))
	if env["data"] == nil || env["skill"] != "station_queue" {
		t.Fatalf("a text part holding the skill JSON must be answered: %v", env)
	}
}

var errDBDown = errors.New("dial tcp 10.0.0.7:5432: connection refused")

// failingReads fails every read with an infrastructure error; orderOnly fails only OrderDetail.
type failingReads struct{ orderOnly bool }

func (f failingReads) Transport(ctx context.Context, id string) (usecases.TransportView, error) {
	if f.orderOnly {
		return fakeReads{}.Transport(ctx, id)
	}
	return usecases.TransportView{}, errDBDown
}

func (failingReads) OrderDetail(context.Context, string) (transportorder.Snapshot, error) {
	return transportorder.Snapshot{}, errDBDown
}

func (failingReads) Track(context.Context, string) (usecases.TrackView, error) {
	return usecases.TrackView{}, errDBDown
}

func (failingReads) Station(context.Context, string) (stationqueue.Snapshot, error) {
	return stationqueue.Snapshot{}, errDBDown
}

func (failingReads) Subsystems(context.Context) ([]capacity.Snapshot, error) { return nil, errDBDown }

func (failingReads) EquipmentUnits(context.Context, string) ([]facts.EquipmentState, error) {
	return nil, errDBDown
}

func TestAnInfrastructureErrorIsAGenericProblemThatLeaksNothing(t *testing.T) {
	client := agent(t, failingReads{})
	for name, args := range map[string]map[string]any{
		"transport_status": {"transportRequestId": "task-1.1"}, "container_position": {"containerId": "TOTE-1"},
		"station_queue": {"stationId": "ST1"}, "subsystem_capacity": {}, "equipment_units": {"asrsId": "ASRS1"},
	} {
		assertInternalError(t, client, name, args)
	}
	assertInternalError(t, agent(t, failingReads{orderOnly: true}), "transport_status", map[string]any{"transportRequestId": "task-1.1"})
}

func assertInternalError(t *testing.T, client *a2aclient.Client, name string, args map[string]any) {
	t.Helper()
	env, text := send(t, client, skill(name, args))
	raw, _ := json.Marshal(env)
	if problemCode(env) != "internal-error" || env["data"] != nil || strings.Contains(string(raw)+text, "10.0.0.7") ||
		text != "Internal server error (500): the skill failed; see the server logs (trace id in the response metadata)" {
		t.Errorf("%s = %s %q", name, raw, text)
	}
}

// rejectedReads answers a REJECTED request: no order, so OrderDetail must not be asked.
type rejectedReads struct{ failingReads }

func (rejectedReads) Transport(_ context.Context, id string) (usecases.TransportView, error) {
	return usecases.TransportView{RequestID: id, ContainerID: "TOTE-9", Status: usecases.ViewRejected, Reason: "UNKNOWN_CONTAINER"}, nil
}

func TestARejectedTransportHasNoLegsAndAsksForNoOrder(t *testing.T) {
	env, text := send(t, agent(t, rejectedReads{}), skill("transport_status", map[string]any{"transportRequestId": "rq-9"}))
	raw, _ := json.Marshal(env["data"])
	if string(raw) != `{"container_id":"TOTE-9","legs":[],"legs_done":0,"legs_total":0,"purpose":"","reason":"UNKNOWN_CONTAINER",`+
		`"retries":0,"status":"REJECTED","transport_request_id":"rq-9"}` || text != "rq-9: REJECTED (UNKNOWN_CONTAINER)" {
		t.Fatalf("rejected = %s %q", raw, text)
	}
}
