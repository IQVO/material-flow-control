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

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"

	inbounda2a "github.com/IQVO/material-flow-control/internal/adapters/inbound/a2a"
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
// the way cmd/api would (test-only wiring: the A2A binary has no write seam; the events go to a memory sink).
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

// askSkill sends one skill request and returns the envelope's data (failing on a problem).
func askSkill(t *testing.T, client *a2aclient.Client, skill string, args map[string]any) map[string]any {
	t.Helper()
	part := a2a.NewDataPart(map[string]any{"skill": skill, "arguments": args})
	resp, err := client.SendMessage(context.Background(), &a2a.SendMessageRequest{Message: a2a.NewMessage(a2a.MessageRoleUser, part)})
	if err != nil {
		t.Fatalf("%s: %v", skill, err)
	}
	msg, ok := resp.(*a2a.Message)
	if !ok || len(msg.Parts) != 2 {
		t.Fatalf("%s: reply = %T %+v", skill, resp, resp)
	}
	raw, _ := json.Marshal(msg.Parts[0].Content.(a2a.Data).Value)
	var env struct {
		Data    map[string]any `json:"data"`
		Problem map[string]any `json:"problem"`
	}
	if err := json.Unmarshal(raw, &env); err != nil || env.Problem != nil || env.Data == nil {
		t.Fatalf("%s: envelope %s (%v)", skill, raw, err)
	}
	return env.Data
}

// The composition root end to end: migrations, the Postgres read repositories, the Reader, the A2A adapter and
// the router, driven by the SDK client built from the served card over JSON-RPC. It reads what the write side
// stored and writes nothing itself.
func TestA2AServesPostgresBackedReadsAndWritesNothing(t *testing.T) {
	url := pgtest.URL(t)
	seedPick(t, url)
	before, sums := rowCounts(t, url), tableFingerprints(t, url)
	reads, closeReads, err := buildReads(context.Background(), config{DatabaseURL: url, MigrationsURL: url, LayoutFile: layoutFile})
	if err != nil {
		t.Fatal(err)
	}
	defer closeReads()
	srv := httptest.NewServer(newAgent(inbounda2a.Deps{Reads: reads}, "http://placeholder/", "it", quiet(), "material-flow-control-a2a"))
	defer srv.Close()
	client, err := a2aclient.NewFromCard(context.Background(), inbounda2a.Card(inbounda2a.Config{AgentURL: srv.URL + "/a2a", Version: "it"}))
	if err != nil {
		t.Fatal(err)
	}

	got := askSkill(t, client, "transport_status", map[string]any{"transportRequestId": "rq-1"})
	legs, _ := got["legs"].([]any)
	if got["status"] != "IN_PROGRESS" || got["container_id"] != "TOTE-1" || len(legs) != 2 ||
		legs[0].(map[string]any)["kind"] != "RETRIEVE" || legs[1].(map[string]any)["kind"] != "MOVE" {
		t.Fatalf("transport_status = %v", got)
	}
	got = askSkill(t, client, "container_position", map[string]any{"containerId": "TOTE-1"})
	if pos, _ := got["position"].(map[string]any); pos["kind"] != "IN_ASRS" || got["active_transport_request_id"] != "rq-1" {
		t.Fatalf("container_position = %v", got)
	}
	got = askSkill(t, client, "station_queue", map[string]any{"stationId": "ST1"})
	if got["station_id"] != "ST1" || got["buffer_limit"] != float64(2) {
		t.Fatalf("station_queue = %v", got)
	}
	if got = askSkill(t, client, "subsystem_capacity", nil); got["subsystems"] == nil {
		t.Fatalf("subsystem_capacity = %v", got)
	}
	if got = askSkill(t, client, "equipment_units", map[string]any{"asrsId": "ASRS1"}); got["asrs_id"] != "ASRS1" {
		t.Fatalf("equipment_units = %v", got)
	}
	assertNoWrites(t, before, rowCounts(t, url))
	assertSameFingerprints(t, "A2A agent", sums, tableFingerprints(t, url))
}

func assertNoWrites(t *testing.T, before, after map[string]int) {
	t.Helper()
	for table, n := range before {
		if after[table] != n {
			t.Errorf("the A2A agent changed %s: %d rows, want %d", table, after[table], n)
		}
	}
	if before["transport_requests"] != 1 || before["subsystem_commands"] == 0 {
		t.Fatalf("the seed did not accept rq-1: %v", before)
	}
}

// tableFingerprints is an md5 of every row of every table of the public schema (migrations bookkeeping included), so
// an UPDATE that keeps a row count is caught too. carry (Task 21 review)
func tableFingerprints(t *testing.T, url string) map[string]string {
	t.Helper()
	ctx := context.Background()
	pool, err := postgres.NewPool(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	rows, err := pool.Query(ctx, `SELECT table_name FROM information_schema.tables WHERE table_schema = 'public' AND table_type = 'BASE TABLE' ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, name)
	}
	rows.Close()
	if len(tables) < 10 {
		t.Fatalf("public tables = %v, want the whole schema", tables)
	}
	out := make(map[string]string, len(tables))
	for _, table := range tables {
		var sum string
		q := `SELECT md5(coalesce(string_agg(r::text, '|' ORDER BY r::text), '')) FROM "` + table + `" r`
		if err := pool.QueryRow(ctx, q).Scan(&sum); err != nil {
			t.Fatal(err)
		}
		out[table] = sum
	}
	return out
}

func assertSameFingerprints(t *testing.T, surface string, before, after map[string]string) {
	t.Helper()
	if len(after) != len(before) {
		t.Fatalf("the %s changed the schema: tables %d, want %d", surface, len(after), len(before))
	}
	for table, sum := range before {
		if after[table] != sum {
			t.Errorf("the %s changed table %s (md5 %s, want %s)", surface, table, after[table], sum)
		}
	}
}
