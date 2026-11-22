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
	"testing"
	"time"

	kafkago "github.com/segmentio/kafka-go"

	"github.com/IQVO/material-flow-control/internal/testing/kafkatest"
	"github.com/IQVO/material-flow-control/internal/testing/pgtest"
)

const (
	catchUpAsrsTopic = "warehouse.asrs-control.events"
	catchUpAmrTopic  = "warehouse.amr-fleet-control.events"
	catchUpBacklog   = 400
)

// ruling P4-R20: material-flow-control decides no transport request and reports no readiness before both subsystem
// consumers caught up with the end offsets sampled at boot. The backlog (capacity of ASRS9 and AMR9, then 400
// ContainerStored, the requested container LAST) sits on the topics before the process starts; a request decided
// mid-replay would be refused UNKNOWN_CONTAINER (or SUBSYSTEM_UNAVAILABLE) for good under its transport_request_id
// (decision D7). The test's own layout (ASRS9/AMR9) keeps its facts out of the e2e test's way on the shared topics.
func TestNoRequestIsDecidedAndReadyzIsNotReadyBeforeTheBacklogIsReplayed(t *testing.T) {
	for _, topic := range []string{catchUpAsrsTopic, catchUpAmrTopic, mfcTopic, "warehouse.material-flow-control.analytics",
		"warehouse.asrs-control.events.dlq", "warehouse.amr-fleet-control.events.dlq"} {
		kafkatest.CreateTopic(t, topic)
	}
	nano := strconv.FormatInt(time.Now().UnixNano(), 36)
	last := publishBacklog(t, nano)
	boot := map[string]int64{catchUpAsrsTopic: kafkatest.EndOffset(t, catchUpAsrsTopic), catchUpAmrTopic: kafkatest.EndOffset(t, catchUpAmrTopic)}
	groups := map[string]string{catchUpAsrsTopic: "catchup-asrs-" + nano, catchUpAmrTopic: "catchup-amr-" + nano}
	base := startCatchUpApp(t, groups)

	body := map[string]any{"transport_request_id": "rq-catchup-" + nano, "container_id": last,
		"source": "WH1-AUTO-ASRS-ASRS9-00-00-S", "destination": "ST9", "purpose": "PICK", "priority": "NORMAL",
		"due_by": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)}
	key := "key-catchup-" + nano
	status, problem := post(t, base+"/transport-requests", body, key)
	readyz, _ := get(t, base+"/readyz")
	if status != http.StatusServiceUnavailable || problem["code"] != "dependency-unavailable" {
		t.Fatalf("POST /transport-requests before the catch-up = %d %v, want 503 dependency-unavailable (not decided)", status, problem)
	}
	if readyz != http.StatusServiceUnavailable {
		t.Fatalf("GET /readyz before the catch-up = %d, want 503", readyz)
	}

	var seen any
	deadline := time.Now().Add(90 * time.Second)
	for readyz != http.StatusOK && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
		readyz, seen = get(t, base+"/readyz")
	}
	if readyz != http.StatusOK {
		t.Fatalf("GET /readyz never became ready within 90 s; last answer %d %v", readyz, seen)
	}
	for topic, group := range groups {
		if done := kafkatest.Committed(t, group, topic); done < boot[topic] {
			t.Fatalf("ready while %s committed %d on %s, below the end offset %d sampled at boot", group, done, topic, boot[topic])
		}
	}
	// The refused attempt released its key and recorded nothing: the SAME key and body is now decided, and accepted.
	status, view := post(t, base+"/transport-requests", body, key)
	if status != http.StatusAccepted || view["status"] != "IN_PROGRESS" {
		t.Fatalf("POST /transport-requests after the catch-up = %d %v, want 202 IN_PROGRESS", status, view)
	}
}

// publishBacklog writes the capacity facts, then catchUpBacklog ContainerStored facts; it returns the last container.
func publishBacklog(t *testing.T, nano string) string {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	asrs := []kafkago.Message{subsystemFact(t, catchUpAsrsTopic, "com.warehouse.wes.asrs-control.asrs.AsrsCapacityChanged", "ASRS9",
		map[string]any{"asrs_id": "ASRS9", "units_total": 2, "units_available": 2, "positions_total": 100, "positions_free": 50,
			"open_jobs": 0, "measured_at": now})}
	var last string
	for i := range catchUpBacklog {
		last = fmt.Sprintf("TOTE-%s-%d", nano, i)
		asrs = append(asrs, subsystemFact(t, catchUpAsrsTopic, "com.warehouse.wes.asrs-control.storagejob.ContainerStored", last,
			map[string]any{"storage_job_id": "sj-" + last, "asrs_id": "ASRS9", "container_id": last, "port_id": "IN1", "stored_at": now}))
	}
	amr := subsystemFact(t, catchUpAmrTopic, "com.warehouse.wes.amr-fleet-control.fleet.FleetCapacityChanged", "AMR9",
		map[string]any{"fleet_id": "AMR9", "robots_total": 3, "robots_online": 3, "robots_available": 3, "robots_charging": 0,
			"robots_held": 0, "legs_queued": 0, "legs_active": 0, "changed_at": now})
	writeFacts(t, append(asrs, amr))
	return last
}

func subsystemFact(t *testing.T, topic, typ, subject string, data map[string]any) kafkago.Message {
	t.Helper()
	value, err := json.Marshal(map[string]any{"specversion": "1.0", "id": fmt.Sprintf("%s-%d", subject, time.Now().UnixNano()),
		"source": "/warehouse/catchup-test", "type": typ, "subject": subject, "time": time.Now().UTC().Format(time.RFC3339Nano),
		"datacontenttype": "application/json", "data": data})
	if err != nil {
		t.Fatal(err)
	}
	return kafkago.Message{Topic: topic, Key: []byte(subject), Value: value}
}

func writeFacts(t *testing.T, msgs []kafkago.Message) {
	t.Helper()
	w := &kafkago.Writer{Addr: kafkago.TCP(kafkatest.Brokers(t)...), RequiredAcks: kafkago.RequireAll, BatchTimeout: time.Millisecond,
		WriteTimeout: 10 * time.Second, ReadTimeout: 10 * time.Second}
	defer func() { _ = w.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	for {
		err := w.WriteMessages(ctx, msgs...)
		if err == nil {
			return
		}
		if ctx.Err() != nil {
			t.Fatalf("publish the backlog: %v", err)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// startCatchUpApp wires and runs the real process (wire + run) and returns its base URL.
func startCatchUpApp(t *testing.T, groups map[string]string) string {
	t.Helper()
	layout, _ := filepath.Abs("testdata/catchup/flow-layout.yaml")
	cfg, err := loadConfig(env(map[string]string{"DATABASE_URL": pgtest.URL(t), "KAFKA_BROKERS": kafkatest.Brokers(t)[0],
		"ASRS_CONTROL_URL": "http://127.0.0.1:1", "AMR_FLEET_CONTROL_URL": "http://127.0.0.1:1", "MFC_LAYOUT_FILE": layout,
		"MFC_ASRS_CONSUMER_GROUP": groups[catchUpAsrsTopic], "MFC_AMR_CONSUMER_GROUP": groups[catchUpAmrTopic]}))
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	a, err := wire(ctx, cfg, slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})))
	if err != nil {
		cancel()
		_ = ln.Close()
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- a.run(ctx, ln) }()
	t.Cleanup(func() { cancel(); <-done })
	return "http://" + ln.Addr().String()
}

func post(t *testing.T, url string, body any, key string) (int, map[string]any) {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", key)
	return send(t, req)
}

func get(t *testing.T, url string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, http.NoBody)
	return send(t, req)
}

func send(t *testing.T, req *http.Request) (int, map[string]any) {
	t.Helper()
	res, err := e2eClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", req.Method, req.URL, err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return res.StatusCode, m
}
