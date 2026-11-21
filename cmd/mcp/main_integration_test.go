//go:build integration

package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	inboundmcp "github.com/IQVO/material-flow-control/internal/adapters/inbound/mcp"
	"github.com/IQVO/material-flow-control/internal/adapters/outbound/memory"
	"github.com/IQVO/material-flow-control/internal/adapters/outbound/postgres"
	"github.com/IQVO/material-flow-control/internal/application/usecases"
	layoutconfig "github.com/IQVO/material-flow-control/internal/config"
	ct "github.com/IQVO/material-flow-control/internal/domain/containertrack"
	tr "github.com/IQVO/material-flow-control/internal/domain/transportrequest"
	"github.com/IQVO/material-flow-control/internal/testing/pgtest"
)

const layoutFile = "../../config/flow-layout.example.yaml"

// seedPick accepts one PICK of a stored TOTE-1 to ST1 through the write use cases over the same database,
// the way cmd/api would (test-only wiring: the MCP binary has no write seam; the events go to a memory sink).
func seedPick(t *testing.T, url string) {
	t.Helper()
	ctx := context.Background()
	l, err := layoutconfig.LoadLayout(layoutFile)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := postgres.NewPool(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	now := time.Now().UTC()
	svc, err := usecases.NewService(l, usecases.DefaultConfig(), usecases.Deps{UoW: postgres.NewUnitOfWork(pool),
		Lock: postgres.NewFlowLock(pool), Requests: postgres.NewRequests(pool), Orders: postgres.NewOrders(pool),
		Tracks: postgres.NewTracks(pool), Stations: postgres.NewStations(pool), Capacities: postgres.NewCapacities(pool),
		Units: postgres.NewUnits(pool), Commands: postgres.NewCommands(pool), Inbox: postgres.NewProcessedEvents(pool),
		Sink: memory.NewEvents(memory.NewStore()), Clock: memory.NewClock(now), IDs: memory.NewIDs("it-"),
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	track := ct.New("TOTE-1", now)
	track.MoveTo(ct.Position{Kind: ct.InAsrs, Code: "WH1-AUTO-ASRS-ASRS1-00-00-S"}, now)
	if err := postgres.NewTracks(pool).Save(ctx, track); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RequestTransport(ctx, tr.Request{ID: "rq-1", ContainerID: "TOTE-1", Source: "WH1-AUTO-ASRS-ASRS1-00-00-S",
		Destination: "WH1-GTP-ST1", Purpose: tr.PurposePick, Priority: tr.PriorityNormal, DueBy: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
}

func rowCounts(t *testing.T, url string) map[string]int {
	t.Helper()
	pool, err := postgres.NewPool(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	out := map[string]int{}
	for _, table := range []string{"transport_requests", "transport_orders", "transport_legs", "container_tracks", "container_moves",
		"station_queues", "subsystem_commands", "outbox_events", "idempotency_keys", "processed_events"} {
		var n int
		if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM `+table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		out[table] = n
	}
	return out
}

func callTool(t *testing.T, session *sdk.ClientSession, name, args string) map[string]any {
	t.Helper()
	res, err := session.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: json.RawMessage(args)})
	if err != nil || res.IsError {
		raw, _ := json.Marshal(res)
		t.Fatalf("%s: %v %s", name, err, raw)
	}
	var out map[string]any
	raw, _ := json.Marshal(res.StructuredContent)
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// The composition root end to end: migrations, the Postgres read repositories, the Reader, the MCP adapter
// and the router, driven by the SDK client over Streamable HTTP. It reads what the write side stored and
// writes nothing itself.
func TestMCPServesPostgresBackedReadsAndWritesNothing(t *testing.T) {
	url := pgtest.URL(t)
	seedPick(t, url)
	before := rowCounts(t, url)
	reads, closeReads, err := buildReads(context.Background(), config{DatabaseURL: url, MigrationsURL: url, LayoutFile: layoutFile})
	if err != nil {
		t.Fatal(err)
	}
	defer closeReads()
	srv := httptest.NewServer(newRouter(inboundmcp.Handler(inboundmcp.NewServer(inboundmcp.Deps{Reads: reads})), quiet(), "material-flow-control-mcp"))
	defer srv.Close()
	session, err := sdk.NewClient(&sdk.Implementation{Name: "it", Version: "0"}, nil).
		Connect(context.Background(), &sdk.StreamableClientTransport{Endpoint: srv.URL + "/mcp"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()

	got := callTool(t, session, "get_transport", `{"transportRequestId":"rq-1"}`)
	legs, _ := got["legs"].([]any)
	if got["status"] != "IN_PROGRESS" || got["container_id"] != "TOTE-1" || len(legs) != 2 ||
		legs[0].(map[string]any)["kind"] != "RETRIEVE" || legs[1].(map[string]any)["kind"] != "MOVE" {
		t.Fatalf("get_transport = %v", got)
	}
	got = callTool(t, session, "get_container_track", `{"containerId":"TOTE-1"}`)
	if pos, _ := got["position"].(map[string]any); pos["kind"] != "IN_ASRS" || got["active_transport_request_id"] != "rq-1" {
		t.Fatalf("get_container_track = %v", got)
	}
	got = callTool(t, session, "get_station", `{"stationId":"ST1"}`)
	if got["station_id"] != "ST1" || got["buffer_limit"] != float64(2) {
		t.Fatalf("get_station = %v", got)
	}
	if got = callTool(t, session, "list_subsystems", `{}`); got["subsystems"] == nil {
		t.Fatalf("list_subsystems = %v", got)
	}
	if got = callTool(t, session, "list_equipment_units", `{"asrsId":"ASRS1"}`); got["asrs_id"] != "ASRS1" {
		t.Fatalf("list_equipment_units = %v", got)
	}
	after := rowCounts(t, url)
	for table, n := range before {
		if after[table] != n {
			t.Errorf("the MCP server changed %s: %d rows, want %d", table, after[table], n)
		}
	}
	if before["transport_requests"] != 1 || before["subsystem_commands"] == 0 {
		t.Fatalf("the seed did not accept rq-1: %v", before)
	}
}
