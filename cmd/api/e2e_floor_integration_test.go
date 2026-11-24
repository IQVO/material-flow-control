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

// The e2e floor of plan 04 Task 25 (D23): containers, material-flow-control in-process, and the REST helpers. The
// fact helpers are e2e_facts_integration_test.go; the rows are e2e_subsystems_integration_test.go.

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
	// start is the end offset of the MFC topic before this run's floor booted (ruling P4-R19): a positional read never
	// sees an earlier -count run's facts.
	start     int64
	asrsStart int64 // the asrs-control topic's end before this run's subsystems started
	// marks holds, per accepted order, the MFC topic's end offset taken BEFORE its POST (ruling P4-R19); orders are
	// this run's accepted orders, in order.
	marks  map[string]int64
	orders []string
	// due is every request's due_by, fixed per run: a retry must send the IDENTICAL request, and a due_by computed per
	// call (second resolution) made the idempotent retry a 409 conflict whenever the two calls straddled a second.
	// carry (Task 25 review)
	due string
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

// amrMap is amr-fleet-control's pinned example map, which carries PK1/FWD1/CLR1 since plan 06 (map version
// 2026-11-22.1; the plan Q6 inline node additions are gone): the bytes are written UNCHANGED to a temp file for
// the container. The handoff assertion is the pin-raise guard (plan 06 D6): a checkout still at a pre-plan-06
// map fails HERE with the missing points, not on the floor as a runtime 422 unknown-handoff-point.
func amrMap(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(required(t, "AMR_FLEET_CONTROL_MAP"))
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Nodes []struct {
			HandoffPoint string `yaml:"handoffPoint"`
		} `yaml:"nodes"`
	}
	if err := yaml.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	handoffs := make(map[string]bool, len(m.Nodes))
	for _, n := range m.Nodes {
		if n.HandoffPoint != "" {
			handoffs[n.HandoffPoint] = true
		}
	}
	var missing []string
	for _, id := range []string{"PK1", "FWD1", "CLR1"} {
		if !handoffs[id] {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("pinned amr map lacks handoff point(s): %s (raise the amr-fleet-control pins in contracts/pinned.json and the ci.yml integration checkout ref together, plan 06 D6)", strings.Join(missing, ", "))
	}
	path := filepath.Join(t.TempDir(), "map.yaml")
	if err := os.WriteFile(path, b, 0o644); err != nil { //nolint:gosec // read by the container's non-root user
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
	f := &floor{t: t, nonce: strconv.FormatInt(time.Now().UnixNano(), 36), marks: map[string]int64{},
		due: time.Now().Add(time.Hour).UTC().Format(time.RFC3339)}
	brokers := strings.Join(kafkatest.Brokers(t), ",")
	for _, topic := range []string{"warehouse.asrs-control.events", "warehouse.asrs-control.analytics", "warehouse.amr-fleet-control.events",
		"warehouse.amr-fleet-control.analytics", mfcTopic, "warehouse.material-flow-control.analytics",
		"warehouse.asrs-control.events.dlq", "warehouse.amr-fleet-control.events.dlq"} {
		kafkatest.CreateTopic(t, topic)
	}
	f.start = kafkatest.EndOffset(t, mfcTopic) // ruling P4-R19
	f.asrsStart = kafkatest.EndOffset(t, "warehouse.asrs-control.events")
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
	f.caughtUp(map[string]string{"warehouse.asrs-control.events": "e2e-asrs-" + nano, "warehouse.amr-fleet-control.events": "e2e-amr-" + nano})
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
		"source": src, "destination": dst, "purpose": purpose, "priority": "NORMAL", "due_by": f.due},
		"e2e-"+strconv.Itoa(f.n))
}

// accept POSTs a request that must be accepted and returns its order. The MFC topic's end offset taken BEFORE the POST
// is the order's mark: every fact wait on the order reads from it (ruling P4-R19).
func (f *floor) accept(id, container, src, dst, purpose string) string {
	f.t.Helper()
	mark := kafkatest.EndOffset(f.t, mfcTopic)
	code, m := f.transport(id, container, src, dst, purpose)
	order, ok := m["transport_order_id"].(string)
	if code != http.StatusAccepted || !ok {
		f.t.Fatalf("%s = %d %v", id, code, m)
	}
	f.marks[order] = mark
	f.orders = append(f.orders, order)
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
