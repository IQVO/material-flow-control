//go:build integration

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"gopkg.in/yaml.v3"

	"github.com/IQVO/material-flow-control/internal/testing/dockernet"
	"github.com/IQVO/material-flow-control/internal/testing/kafkatest"
	"github.com/IQVO/material-flow-control/internal/testing/pgtest"
)

// The e2e test of plan 04 Task 25 (decision D23): the eight acceptance rows against the REAL subsystems, each the
// image built from its released commit (asrs-control v0.1.1 a3e172d, amr-fleet-control v0.1.0 09a8f19,
// equipment-emulator v0.1.0 b58201e; CI's integration job builds them, see .github/workflows/ci.yml).
//
// deviation (Task 25): the brief ran asrs-control and amr-fleet-control as host binaries because kafkatest's broker
// advertised only 127.0.0.1. They run as their release IMAGES instead: every container joins the test binary's one
// dockernet network, on which the broker is "kafka:9092" (kafkatest.InternalBrokers) and Postgres is "postgres:5432"
// (pgtest.NetworkURL); material-flow-control runs in-process on the mapped 127.0.0.1 ports.

const (
	mfcTopic = "warehouse.material-flow-control.events"
	asrsLoc  = "WH1-AUTO-ASRS-ASRS1-00-00-S"
	decant   = "WH1-RCV-DEC1"
)

// e2eClient bounds every call, so a hung request fails on its own line.
var e2eClient = &http.Client{Timeout: 15 * time.Second}

type floor struct {
	t                *testing.T
	mfc, emu, amrAPI string
	n                int
	nonce            string
}

// id makes a container or request id unique to this run: with -count=N the binary's one broker still holds the
// earlier runs' facts, which a new consumer group reads from the first offset.
func (f *floor) id(s string) string { return s + "-" + f.nonce }

// required is an environment variable CI's integration job sets (the image built from the pinned commit).
func required(t *testing.T, name string) string {
	t.Helper()
	v := os.Getenv(name)
	if v == "" {
		t.Fatalf("%s is required: CI's integration job builds it from the pinned commit; locally see docs/docs/development/testing.md (e2e)", name)
	}
	return v
}

// run starts one container on the shared network and returns it with its mapped 8080 endpoint (http://127.0.0.1:p).
func (f *floor) run(name string, req testcontainers.ContainerRequest) (testcontainers.Container, string) {
	f.t.Helper()
	ctx := context.Background()
	req.Networks = []string{dockernet.Name(f.t)}
	if len(req.ExposedPorts) == 0 {
		req.ExposedPorts = []string{"8080/tcp"}
	}
	if req.WaitingFor == nil {
		req.WaitingFor = wait.ForHTTP("/readyz").WithPort("8080/tcp").WithStartupTimeout(120 * time.Second)
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{Started: true, ContainerRequest: req})
	if c != nil {
		f.t.Cleanup(func() { f.dumpOnFailure(name, c); _ = c.Terminate(ctx) })
	}
	if err != nil {
		f.t.Fatalf("start %s (%s): %v", name, req.Image, err)
	}
	if req.ExposedPorts[0] != "8080/tcp" {
		return c, ""
	}
	ep, err := c.PortEndpoint(ctx, "8080/tcp", "http")
	if err != nil {
		f.t.Fatal(err)
	}
	return c, strings.Replace(ep, "localhost", "127.0.0.1", 1)
}

// dumpOnFailure writes the tail of a container's log when the test failed: the suite's first runs are in CI.
func (f *floor) dumpOnFailure(name string, c testcontainers.Container) {
	if !f.t.Failed() {
		return
	}
	r, err := c.Logs(context.Background())
	if err != nil {
		return
	}
	defer r.Close()
	b, _ := io.ReadAll(r)
	if len(b) > 64<<10 {
		b = b[len(b)-64<<10:]
	}
	fmt.Fprintf(os.Stderr, "===== %s log (tail) =====\n%s\n===== end %s =====\n", name, b, name)
}

// startEmulator runs Mosquitto and the pinned emulator image on the shared network.
func (f *floor) startEmulator() {
	f.t.Helper()
	f.run("mosquitto", testcontainers.ContainerRequest{Image: "eclipse-mosquitto:2.0.20", ExposedPorts: []string{"1883/tcp"},
		Cmd: []string{"mosquitto", "-c", "/mosquitto-no-auth.conf"}, NetworkAliases: map[string][]string{dockernet.Name(f.t): {"mosquitto"}},
		WaitingFor: wait.ForListeningPort("1883/tcp")})
	world, _ := filepath.Abs("testdata/e2e/world.yaml")
	_, f.emu = f.run("equipment-emulator", testcontainers.ContainerRequest{Image: required(f.t, "EQUIPMENT_EMULATOR_IMAGE"),
		Env:        map[string]string{"MQTT_URL": "tcp://mosquitto:1883", "SIM_SPEED": "10", "WORLD_FILE": "/tmp/world.yaml"},
		Files:      []testcontainers.ContainerFile{{HostFilePath: world, ContainerFilePath: "/tmp/world.yaml", FileMode: 0o644}},
		WaitingFor: wait.ForHTTP("/readyz").WithPort("8080/tcp").WithStartupTimeout(60 * time.Second)})
}

// amrMap is amr-fleet-control's pinned example map plus the pack, forward and clarification nodes (plan Q6).
func amrMap(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(required(t, "AMR_FLEET_CONTROL_MAP"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := yaml.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	nodes, _ := m["nodes"].([]any)
	edges, _ := m["edges"].([]any)
	for _, n := range []map[string]any{{"id": "PK1", "x": 2, "y": 8, "kind": "STATION", "handoffPoint": "PK1"},
		{"id": "FWD1", "x": 4, "y": 8, "kind": "STATION", "handoffPoint": "FWD1"},
		{"id": "CLR1", "x": 8, "y": 6, "kind": "STATION", "handoffPoint": "CLR1"}} {
		nodes = append(nodes, n)
	}
	for _, e := range [][2]string{{"F", "PK1"}, {"G", "FWD1"}, {"H", "CLR1"}} {
		edges = append(edges, map[string]any{"id": e[0] + "-" + e[1], "a": e[0], "b": e[1]})
	}
	m["nodes"], m["edges"] = nodes, edges
	out, _ := yaml.Marshal(m)
	path := filepath.Join(t.TempDir(), "map.yaml")
	if err := os.WriteFile(path, out, 0o644); err != nil { //nolint:gosec // read by the container's non-root user
		t.Fatal(err)
	}
	return path
}

// startSubsystems runs asrs-control and amr-fleet-control, each on an EMPTY database (their migrations run at boot).
func (f *floor) startSubsystems() (asrsURL, amrURL string) {
	f.t.Helper()
	brokers := strings.Join(kafkatest.InternalBrokers(f.t), ",")
	layout, _ := filepath.Abs("testdata/e2e/asrs-layout.yaml")
	_, asrsURL = f.run("asrs-control", testcontainers.ContainerRequest{Image: required(f.t, "ASRS_CONTROL_IMAGE"),
		Env: map[string]string{"DATABASE_URL": pgtest.NetworkURL(f.t, pgtest.EmptyURL(f.t)), "KAFKA_BROKERS": brokers,
			"MQTT_URL": "tcp://mosquitto:1883", "MQTT_CLIENT_ID": "asrs-control-e2e", "ASRS_LAYOUT_FILE": "/tmp/layout.yaml",
			"OUTBOX_INTERVAL_MS": "50"},
		Files: []testcontainers.ContainerFile{{HostFilePath: layout, ContainerFilePath: "/tmp/layout.yaml", FileMode: 0o644}}})
	_, amrURL = f.run("amr-fleet-control", testcontainers.ContainerRequest{Image: required(f.t, "AMR_FLEET_CONTROL_IMAGE"),
		Env: map[string]string{"DATABASE_URL": pgtest.NetworkURL(f.t, pgtest.EmptyURL(f.t)), "KAFKA_BROKERS": brokers,
			"MQTT_URL": "tcp://mosquitto:1883", "MQTT_CLIENT_ID": "amr-fleet-control-e2e", "AMR_MAP_FILE": "/tmp/map.yaml",
			"OUTBOX_INTERVAL_MS": "50"},
		Files: []testcontainers.ContainerFile{{HostFilePath: amrMap(f.t), ContainerFilePath: "/tmp/map.yaml", FileMode: 0o644}}})
	return asrsURL, amrURL
}

func startFloor(t *testing.T) *floor {
	t.Helper()
	ctx := context.Background()
	f := &floor{t: t, nonce: strconv.FormatInt(time.Now().UnixNano(), 36)}
	brokers := strings.Join(kafkatest.Brokers(t), ",")
	for _, topic := range []string{"warehouse.asrs-control.events", "warehouse.asrs-control.analytics", "warehouse.amr-fleet-control.events",
		"warehouse.amr-fleet-control.analytics", mfcTopic, "warehouse.material-flow-control.analytics",
		"warehouse.asrs-control.events.dlq", "warehouse.amr-fleet-control.events.dlq"} {
		kafkatest.CreateTopic(t, topic)
	}
	f.startEmulator()
	asrsURL, amrURL := f.startSubsystems()
	f.amrAPI = amrURL
	layout, _ := filepath.Abs("testdata/e2e/flow-layout.yaml")
	db := pgtest.URL(t)
	nano := strconv.FormatInt(time.Now().UnixNano(), 10)
	cfg, err := loadConfig(env(map[string]string{"DATABASE_URL": db, "KAFKA_BROKERS": brokers, "ASRS_CONTROL_URL": asrsURL,
		"AMR_FLEET_CONTROL_URL": amrURL, "MFC_LAYOUT_FILE": layout, "OUTBOX_INTERVAL_MS": "50",
		"MFC_ASRS_CONSUMER_GROUP": "e2e-asrs-" + nano, "MFC_AMR_CONSUMER_GROUP": "e2e-amr-" + nano}))
	if err != nil {
		t.Fatal(err)
	}
	runCtx, cancel := context.WithCancel(ctx)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	a, err := wire(runCtx, cfg, slog.New(slog.NewJSONHandler(os.Stderr, nil)))
	if err != nil {
		_ = ln.Close()
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- a.run(runCtx, ln) }()
	t.Cleanup(func() { cancel(); <-done })
	f.mfc = "http://" + ln.Addr().String()
	return f
}

// eventually polls ok; on timeout it fails with what seen last observed.
func (f *floor) eventually(what string, timeout time.Duration, ok func() bool, seen func() any) {
	f.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	f.t.Fatalf("%s did not happen within %s; last seen: %v", what, timeout, seen())
}

func (f *floor) call(method, url string, body any, key string) (int, map[string]any) {
	f.t.Helper()
	var rd io.Reader = http.NoBody
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, url, rd)
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	res, err := e2eClient.Do(req)
	if err != nil {
		f.t.Fatalf("%s %s: %v", method, url, err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return res.StatusCode, m
}

// transport POSTs a transport request under a fresh Idempotency-Key.
func (f *floor) transport(id, container, src, dst, purpose string) (int, map[string]any) {
	f.t.Helper()
	f.n++
	return f.call(http.MethodPost, f.mfc+"/transport-requests", map[string]any{"transport_request_id": id, "container_id": container,
		"source": src, "destination": dst, "purpose": purpose, "priority": "NORMAL", "due_by": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)},
		"e2e-"+strconv.Itoa(f.n))
}

func (f *floor) accept(id, container, src, dst, purpose string) string {
	f.t.Helper()
	code, m := f.transport(id, container, src, dst, purpose)
	order, ok := m["transport_order_id"].(string)
	if code != http.StatusAccepted || !ok {
		f.t.Fatalf("%s = %d %v", id, code, m)
	}
	return order
}

func (f *floor) fault(kind, target, port string, active bool) {
	f.t.Helper()
	if code, m := f.call(http.MethodPost, f.emu+"/faults", map[string]any{"kind": kind, "target": target, "port": port, "active": active}, ""); code != http.StatusNoContent {
		f.t.Fatalf("fault %s = %d %v", kind, code, m)
	}
}

// track polls GET /container-tracks/{id} until ok holds for its position.
func (f *floor) track(container string, timeout time.Duration, ok func(position map[string]any) bool) {
	f.t.Helper()
	var last map[string]any
	f.eventually("track of "+container, timeout, func() bool {
		_, last = f.call(http.MethodGet, f.mfc+"/container-tracks/"+container, nil, "")
		p, _ := last["position"].(map[string]any)
		return ok(p)
	}, func() any { return last })
}

// facts reads warehouse.material-flow-control.events until holds (kafkatest dumps the keys read on a timeout).
func facts(t *testing.T, until func([]map[string]any) bool, timeout time.Duration) []map[string]any {
	t.Helper()
	var out []map[string]any
	kafkatest.ReadAll(t, mfcTopic, func(ms []kafkatest.Message) bool {
		out = out[:0]
		for _, m := range ms {
			var ev map[string]any
			_ = json.Unmarshal(m.Value, &ev)
			out = append(out, ev)
		}
		return until(out)
	}, timeout)
	return out
}

func named(evs []map[string]any, subject, name string) []map[string]any {
	var out []map[string]any
	for _, ev := range evs {
		typ, _ := ev["type"].(string)
		if (subject == "" || ev["subject"] == subject) && strings.HasSuffix(typ, "."+name) {
			out = append(out, ev)
		}
	}
	return out
}

func data(ev map[string]any) map[string]any {
	d, _ := ev["data"].(map[string]any)
	return d
}

// ended waits for the one outcome of an order and returns it.
func (f *floor) ended(orderID string, timeout time.Duration) map[string]any {
	f.t.Helper()
	evs := facts(f.t, func(evs []map[string]any) bool {
		return len(named(evs, orderID, "TransportCompleted"))+len(named(evs, orderID, "TransportFailed")) > 0
	}, timeout)
	all := append(named(evs, orderID, "TransportCompleted"), named(evs, orderID, "TransportFailed")...)
	if len(all) != 1 {
		f.t.Fatalf("order %s ended %d times: %v", orderID, len(all), all)
	}
	return all[0]
}

// completed asserts the order ended with TransportCompleted.
func (f *floor) completed(what, orderID string, timeout time.Duration) {
	f.t.Helper()
	if ev := f.ended(orderID, timeout); !strings.HasSuffix(fmt.Sprint(ev["type"]), ".TransportCompleted") {
		f.t.Fatalf("%s (order %s) = %v", what, orderID, ev)
	}
}

func TestTheFloorFilesDescribeOneFloor(t *testing.T) {
	if a, b := readFile(t, "testdata/e2e/flow-layout.yaml"), readFile(t, "../../config/flow-layout.example.yaml"); !bytes.Equal(a, b) {
		t.Fatal("testdata/e2e/flow-layout.yaml differs from config/flow-layout.example.yaml")
	}
	// deviation (Task 25): asrs-control's layout decoder refuses unknown fields (KnownFields), so the emulator world
	// (which has `robots`) is not its layout; asrs-layout.yaml is the world's `asrs` block, held equal here.
	var world, layout map[string]any
	if err := yaml.Unmarshal(readFile(t, "testdata/e2e/world.yaml"), &world); err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(readFile(t, "testdata/e2e/asrs-layout.yaml"), &layout); err != nil {
		t.Fatal(err)
	}
	if len(layout) != 1 || !reflect.DeepEqual(layout["asrs"], world["asrs"]) {
		t.Fatalf("asrs-layout.yaml = %v, want exactly the world's asrs block %v", layout, world["asrs"])
	}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestTheEightAcceptanceRowsAgainstTheRealSubsystems(t *testing.T) {
	f := startFloor(t)
	f.putaway()
	pick, repl := f.goodsToPersonIdempotencyAndRejection()
	f.completed("pick", pick, 120*time.Second)
	f.completed("replenishment", repl, 240*time.Second)
	// deviation (Task 25): the brief's `until` returned true at once, so ReadAll read NOTHING and counted 0; read up to the
	// pick's outcome (already published, after its TransportAccepted) and count there.
	evs := facts(t, func(evs []map[string]any) bool { return len(named(evs, pick, "TransportCompleted")) == 1 }, 30*time.Second)
	if n := len(named(evs, pick, "TransportAccepted")); n != 1 {
		t.Fatalf("the idempotent retry created %d orders", n)
	}
	// The rejection row: repl-2 is TransportRejected CONTAINER_BUSY (subject = the request id), and no order was made.
	if rej := named(evs, f.id("repl-2"), "TransportRejected"); len(rej) != 1 || data(rej[0])["reason"] != "CONTAINER_BUSY" {
		t.Fatalf("TransportRejected of repl-2 = %v", rej)
	}
	for _, ev := range named(evs, "", "TransportAccepted") {
		if data(ev)["transport_request_id"] == f.id("repl-2") {
			t.Fatalf("the busy request made an order: %v", ev)
		}
	}
	// Goods-to-person, second half: the RETURN of the picked container stores it again.
	f.completed("return", f.accept(f.id("ret-1"), f.id("TOTE-1"), "ST1", asrsLoc, "RETURN"), 180*time.Second)
	// Outbound: from the forward area to the pack station.
	f.completed("outbound", f.accept(f.id("out-1"), f.id("TOTE-2"), "WH1-FWD-FWD1", "WH1-PCK-PK1", "OUTBOUND"), 180*time.Second)
	f.clarification()
	f.capacityLoop()
}

// putaway: TOTE-1..3 from the decant handoff into the AS/RS (TOTE-3 is the clarification row's container).
func (f *floor) putaway() {
	f.t.Helper()
	for _, c := range []string{"TOTE-1", "TOTE-2", "TOTE-3"} {
		order := f.accept(f.id("put-"+c), f.id(c), decant, asrsLoc, "PUTAWAY")
		f.completed("putaway "+c, order, 180*time.Second)
		f.track(f.id(c), 10*time.Second, func(p map[string]any) bool { return p["kind"] == "IN_ASRS" })
	}
}

// goodsToPersonIdempotencyAndRejection: PICK TOTE-1 to ST1 (retried under a new key: the same order), and TOTE-2
// requested twice while the first request is in flight (422 CONTAINER_BUSY). It returns the two order ids.
func (f *floor) goodsToPersonIdempotencyAndRejection() (pick, repl string) {
	f.t.Helper()
	pick = f.accept(f.id("pick-1"), f.id("TOTE-1"), asrsLoc, "ST1", "PICK")
	if code, again := f.transport(f.id("pick-1"), f.id("TOTE-1"), asrsLoc, "ST1", "PICK"); code != http.StatusAccepted || again["transport_order_id"] != pick {
		f.t.Fatalf("idempotent retry = %d %v, want 202 with order %s", code, again, pick)
	}
	repl = f.accept(f.id("repl-1"), f.id("TOTE-2"), asrsLoc, "WH1-FWD-FWD1", "REPLENISH")
	if code, rej := f.transport(f.id("repl-2"), f.id("TOTE-2"), asrsLoc, "WH1-FWD-FWD1", "REPLENISH"); code != http.StatusUnprocessableEntity || rej["reason"] != "CONTAINER_BUSY" {
		f.t.Fatalf("busy = %d %v", code, rej)
	}
	evs := facts(f.t, func(evs []map[string]any) bool { return len(named(evs, pick, "ContainerArrivedAtStation")) == 1 }, 240*time.Second)
	if d := data(named(evs, pick, "ContainerArrivedAtStation")[0]); d["station_id"] != "ST1" || d["queue_position"] != 1.0 {
		f.t.Fatalf("arrival = %v", d)
	}
	return pick, repl
}

// clarification: the output ports cannot read; PICK TOTE-3 fails CLARIFICATION, the container goes to CLR1.
// Both output ports get ASRS_NO_READ because asrs-control picks the output port of TOTE-3's aisle.
func (f *floor) clarification() {
	f.t.Helper()
	f.fault("ASRS_NO_READ", "ASRS1", "OUT1", true)
	f.fault("ASRS_NO_READ", "ASRS1", "OUT2", true)
	ev := f.ended(f.accept(f.id("pick-3"), f.id("TOTE-3"), asrsLoc, "ST1", "PICK"), 180*time.Second)
	if !strings.HasSuffix(fmt.Sprint(ev["type"]), ".TransportFailed") || data(ev)["reason"] != "CLARIFICATION" {
		f.t.Fatalf("clarification outcome = %v", ev)
	}
	evs := facts(f.t, func(evs []map[string]any) bool {
		return len(named(evs, f.id("TOTE-3"), "ContainerSentToClarification")) >= 1
	}, 30*time.Second)
	if d := data(named(evs, f.id("TOTE-3"), "ContainerSentToClarification")[0]); d["reason"] != "NO_READ" {
		f.t.Fatalf("sent to clarification = %v", d)
	}
	f.track(f.id("TOTE-3"), 180*time.Second, func(p map[string]any) bool {
		return p["kind"] == "AT_CLARIFICATION" && p["code"] == "WH1-CLR-CLR1"
	})
	f.fault("ASRS_NO_READ", "ASRS1", "OUT1", false)
	f.fault("ASRS_NO_READ", "ASRS1", "OUT2", false)
}

// capacityLoop: R2, one of three robots, drops off the network; within 60 s the fleet's capacity is DEGRADED, one unit
// lower than before.
func (f *floor) capacityLoop() {
	f.t.Helper()
	// deviation (Task 25): the brief took the FIRST capacity fact as "before" (the boot-time UNAVAILABLE 0 of 0, so no
	// later fact was ever lower); "before" is the idle fleet once the clarification move is done: all units available.
	before := lastFleet(facts(f.t, func(evs []map[string]any) bool {
		d := lastFleet(evs)
		return d["status"] == "AVAILABLE" && d["units_total"] == 3.0 && d["units_available"] == 3.0
	}, 60*time.Second))
	f.fault("ROBOT_DISCONNECT", "R2", "", true)
	var seen []map[string]any
	f.t.Cleanup(func() {
		if f.t.Failed() {
			_, fleet := f.call(http.MethodGet, f.amrAPI+"/fleets/AMR1", nil, "")
			fmt.Fprintf(os.Stderr, "capacity loop: before %v; AMR_FLEET/AMR1 capacity facts %v; amr-fleet-control GET /fleets/AMR1 %v\n", before, seen, fleet)
		}
	})
	facts(f.t, func(evs []map[string]any) bool {
		seen = seen[:0]
		for _, ev := range named(evs, "AMR_FLEET/AMR1", "SubsystemCapacityChanged") {
			seen = append(seen, data(ev))
		}
		d := lastFleet(evs)
		got, _ := d["units_available"].(float64)
		want, _ := before["units_available"].(float64)
		return d["status"] == "DEGRADED" && got == want-1
	}, 60*time.Second)
}

func lastFleet(evs []map[string]any) map[string]any {
	all := named(evs, "AMR_FLEET/AMR1", "SubsystemCapacityChanged")
	if len(all) == 0 {
		return map[string]any{"status": "", "units_available": 0.0}
	}
	return data(all[len(all)-1])
}
