package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/IQVO/material-flow-control/internal/application/facts"
	"github.com/IQVO/material-flow-control/internal/application/usecases"
	"github.com/IQVO/material-flow-control/internal/domain/capacity"
	"github.com/IQVO/material-flow-control/internal/domain/stationqueue"
	"github.com/IQVO/material-flow-control/internal/domain/transportorder"
)

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

// rejectedReads answers a REJECTED request: no order, so OrderDetail must not be asked.
type rejectedReads struct{ failingReads }

func (rejectedReads) Transport(_ context.Context, id string) (usecases.TransportView, error) {
	return usecases.TransportView{RequestID: id, ContainerID: "TOTE-9", Status: usecases.ViewRejected, Reason: "UNKNOWN_CONTAINER"}, nil
}

func errorContent(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	b, _ := json.Marshal(res.Content)
	return string(b)
}

func TestAnInfrastructureErrorIsAGenericToolErrorThatLeaksNothing(t *testing.T) {
	cs := session(t, Deps{Reads: failingReads{}})
	for name, args := range map[string]map[string]any{
		"get_transport": {"transportRequestId": "task-1.1"}, "get_container_track": {"containerId": "TOTE-1"},
		"get_station": {"stationId": "ST1"}, "list_subsystems": {}, "list_equipment_units": {"asrsId": "ASRS1"},
	} {
		res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		text := errorContent(t, res)
		if !res.IsError || !strings.Contains(text, "internal-error: an unexpected internal error occurred") || strings.Contains(text, "10.0.0.7") {
			t.Errorf("%s = %v %s", name, res.IsError, text)
		}
	}
}

func TestATransportWhoseOrderCannotBeReadFails(t *testing.T) {
	res, err := session(t, Deps{Reads: failingReads{orderOnly: true}}).CallTool(context.Background(),
		&mcp.CallToolParams{Name: "get_transport", Arguments: map[string]any{"transportRequestId": "task-1.1"}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(errorContent(t, res), "internal-error") {
		t.Fatalf("order read failure = %+v", res)
	}
}

func TestARejectedTransportHasNoLegsAndAsksForNoOrder(t *testing.T) {
	res, err := session(t, Deps{Reads: rejectedReads{}}).CallTool(context.Background(),
		&mcp.CallToolParams{Name: "get_transport", Arguments: map[string]any{"transportRequestId": "rq-9"}})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(res.StructuredContent)
	if res.IsError || string(b) != `{"container_id":"TOTE-9","legs":[],"legs_done":0,"legs_total":0,"purpose":"","reason":"UNKNOWN_CONTAINER",`+
		`"retries":0,"status":"REJECTED","transport_request_id":"rq-9"}` {
		t.Fatalf("rejected = %v %s", res.IsError, b)
	}
}

func TestAnUnknownTransportIsTransportRequestNotFound(t *testing.T) {
	res, err := session(t, Deps{Reads: fakeReads{}}).CallTool(context.Background(),
		&mcp.CallToolParams{Name: "get_transport", Arguments: map[string]any{"transportRequestId": "nope"}})
	if err != nil {
		t.Fatal(err)
	}
	if text := errorContent(t, res); !res.IsError || !strings.Contains(text, "transport-request-not-found: transport request not found") {
		t.Fatalf("unknown transport = %s", text)
	}
}

func readResource(t *testing.T, d Deps, uri string) (*mcp.ReadResourceResult, error) {
	t.Helper()
	return session(t, d).ReadResource(context.Background(), &mcp.ReadResourceParams{URI: uri})
}

func TestTheStationResourceReadsOneQueue(t *testing.T) {
	res, err := readResource(t, Deps{Reads: fakeReads{}}, "warehouse://material-flow-control/stations/ST1")
	if err != nil || len(res.Contents) != 1 {
		t.Fatalf("read = %+v %v", res, err)
	}
	c := res.Contents[0]
	if c.MIMEType != "application/json" || c.URI != "warehouse://material-flow-control/stations/ST1" ||
		c.Text != `{"station_id":"ST1","buffer_limit":2,"occupied":0,"occupants":[],"waiting":[]}` {
		t.Fatalf("contents = %+v", c)
	}
	if _, err := readResource(t, Deps{Reads: fakeReads{}}, "warehouse://material-flow-control/stations/ST9"); err == nil ||
		!strings.Contains(err.Error(), "station-not-found") {
		t.Fatalf("unknown station = %v", err)
	}
	if _, err := readResource(t, Deps{Reads: fakeReads{}}, "warehouse://material-flow-control/stations/ST1/x"); err == nil {
		t.Fatal("a malformed station URI was read")
	}
}

func TestParseStationURI(t *testing.T) {
	for uri, want := range map[string]string{
		"warehouse://material-flow-control/stations/ST1":    "ST1",
		"warehouse://material-flow-control/stations/":       "",
		"warehouse://material-flow-control/stations/ST1/q":  "",
		"warehouse://asrs-control/stations/ST1":             "",
		"warehouse://material-flow-control/transports/ST1":  "",
		"warehouse://material-flow-control/stations/WH1-ST": "WH1-ST",
	} {
		got, ok := parseStationURI(uri)
		if got != want || ok != (want != "") {
			t.Errorf("parseStationURI(%q) = %q %v, want %q", uri, got, ok, want)
		}
	}
}

func TestTheReviewTransportPromptNamesTheRequestAndOnlyReadTools(t *testing.T) {
	cs := session(t, Deps{Reads: fakeReads{}})
	for arg, want := range map[string]string{"task-1.1": "get_transport(transportRequestId = task-1.1)",
		"  ": "get_transport(transportRequestId = <transportRequestId>)"} {
		res, err := cs.GetPrompt(context.Background(), &mcp.GetPromptParams{Name: "review_transport",
			Arguments: map[string]string{"transportRequestId": arg}})
		if err != nil || len(res.Messages) != 1 {
			t.Fatalf("prompt = %+v %v", res, err)
		}
		text := res.Messages[0].Content.(*mcp.TextContent).Text
		if res.Messages[0].Role != "user" || !strings.Contains(text, want) || !strings.Contains(text, "get_container_track") ||
			!strings.Contains(text, "get_station") || !strings.Contains(text, "never assume state") {
			t.Fatalf("prompt text for %q = %s", arg, text)
		}
	}
}
