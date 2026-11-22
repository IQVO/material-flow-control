//go:build integration

package main

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/IQVO/material-flow-control/internal/testing/kafkatest"
)

// The e2e test of plan 04 Task 25 (decision D23): the eight acceptance rows against the REAL subsystems, each the
// image built from its released commit (asrs-control v0.1.1 a3e172d, amr-fleet-control v0.1.0 09a8f19,
// equipment-emulator v0.1.0 b58201e; CI's integration job builds them, see .github/workflows/ci.yml).
//
// deviation (Task 25): the brief ran asrs-control and amr-fleet-control as host binaries because kafkatest's broker
// advertised only 127.0.0.1. They run as their release IMAGES instead: every container joins the test binary's one
// dockernet network, on which the broker is "kafka:9092" (kafkatest.InternalBrokers) and Postgres is "postgres:5432"
// (pgtest.NetworkURL); material-flow-control runs in-process on the mapped 127.0.0.1 ports.
//
// ruling P4-R19: every fact wait reads from a mark taken before its stimulus (e2e_facts_integration_test.go).

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
	pick, repl, busy := f.goodsToPersonIdempotencyAndRejection()
	f.completed("pick", pick, 120*time.Second)
	f.completed("replenishment", repl, 240*time.Second)
	f.idempotency(pick)
	f.rejection(busy)
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
// requested twice while the first request is in flight (422 CONTAINER_BUSY). It returns the two order ids and the
// mark taken before the busy request.
func (f *floor) goodsToPersonIdempotencyAndRejection() (pick, repl string, busy int64) {
	f.t.Helper()
	pick = f.accept(f.id("pick-1"), f.id("TOTE-1"), asrsLoc, "ST1", "PICK")
	if code, again := f.transport(f.id("pick-1"), f.id("TOTE-1"), asrsLoc, "ST1", "PICK"); code != http.StatusAccepted || again["transport_order_id"] != pick {
		f.t.Fatalf("idempotent retry = %d %v, want 202 with order %s", code, again, pick)
	}
	repl = f.accept(f.id("repl-1"), f.id("TOTE-2"), asrsLoc, "WH1-FWD-FWD1", "REPLENISH")
	busy = kafkatest.EndOffset(f.t, mfcTopic) // ruling P4-R19
	if code, rej := f.transport(f.id("repl-2"), f.id("TOTE-2"), asrsLoc, "WH1-FWD-FWD1", "REPLENISH"); code != http.StatusUnprocessableEntity || rej["reason"] != "CONTAINER_BUSY" {
		f.t.Fatalf("busy = %d %v", code, rej)
	}
	evs := factsFrom(f.t, f.marks[pick], func(evs []map[string]any) bool { return len(named(evs, pick, "ContainerArrivedAtStation")) == 1 }, 240*time.Second)
	if d := data(named(evs, pick, "ContainerArrivedAtStation")[0]); d["station_id"] != "ST1" || d["queue_position"] != 1.0 {
		f.t.Fatalf("arrival = %v", d)
	}
	return pick, repl, busy
}

// idempotency: the retried PICK made one order. The read runs from the pick's mark up to its outcome.
// deviation (Task 25): the brief's `until` returned true at once, so it read NOTHING and counted 0.
// carry (Task 25 review): count the TransportAccepted facts of the REQUEST (data.transport_request_id), not of the
// order: a retry that made a second order publishes it under a new order id, which a count by subject never sees.
func (f *floor) idempotency(pick string) {
	f.t.Helper()
	evs := factsFrom(f.t, f.marks[pick], func(evs []map[string]any) bool { return len(named(evs, pick, "TransportCompleted")) == 1 }, 30*time.Second)
	var orders []any
	for _, ev := range named(evs, "", "TransportAccepted") {
		if data(ev)["transport_request_id"] == f.id("pick-1") {
			orders = append(orders, ev["subject"])
		}
	}
	if len(orders) != 1 || orders[0] != pick {
		f.t.Fatalf("TransportAccepted of request pick-1 after its mark = orders %v, want exactly [%s]: the idempotent retry made another order", orders, pick)
	}
}

// rejection: repl-2 is TransportRejected CONTAINER_BUSY (subject = the request id), and no order was made. The window
// runs from the mark taken before the busy request to now (both completions are past it).
// ruling P4-R19
func (f *floor) rejection(busy int64) {
	f.t.Helper()
	evs := f.window(busy, kafkatest.EndOffset(f.t, mfcTopic))
	if rej := named(evs, f.id("repl-2"), "TransportRejected"); len(rej) != 1 || data(rej[0])["reason"] != "CONTAINER_BUSY" {
		f.t.Fatalf("TransportRejected of repl-2 after its mark = %v", rej)
	}
	for _, ev := range named(evs, "", "TransportAccepted") {
		if data(ev)["transport_request_id"] == f.id("repl-2") {
			f.t.Fatalf("the busy request made an order: %v", ev)
		}
	}
}

// clarification: the output ports cannot read; PICK TOTE-3 fails CLARIFICATION, the container goes to CLR1.
// Both output ports get ASRS_NO_READ because asrs-control picks the output port of TOTE-3's aisle.
func (f *floor) clarification() {
	f.t.Helper()
	f.fault("ASRS_NO_READ", "ASRS1", "OUT1", true)
	f.fault("ASRS_NO_READ", "ASRS1", "OUT2", true)
	order := f.accept(f.id("pick-3"), f.id("TOTE-3"), asrsLoc, "ST1", "PICK")
	ev := f.ended(order, 180*time.Second)
	if !strings.HasSuffix(fmt.Sprint(ev["type"]), ".TransportFailed") || data(ev)["reason"] != "CLARIFICATION" {
		f.t.Fatalf("clarification outcome = %v", ev)
	}
	evs := factsFrom(f.t, f.marks[order], func(evs []map[string]any) bool { // ruling P4-R19
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
// ruling P4-R19 (Task 25 review, CRITICAL): the loop is POSITIONAL. "before" is the last AMR_FLEET/AMR1 capacity fact
// before a mark (this run's facts only, from f.start), which must be the idle fleet, AVAILABLE 3 of 3 (else retake the
// mark); the DEGRADED fact must come AFTER that mark, which is taken before the disconnect. A re-read from offset 0
// used to stop at the boot ramp's DEGRADED 2 of 3 and passed with the disconnect removed.
func (f *floor) capacityLoop() {
	f.t.Helper()
	var mark int64
	var before map[string]any
	var window, seen []map[string]any
	f.t.Cleanup(func() {
		if f.t.Failed() {
			_, fleet := f.call(http.MethodGet, f.amrAPI+"/fleets/AMR1", nil, "")
			fmt.Fprintf(os.Stderr, "capacity loop: mark %d; before %v; capacity facts before the mark %v; after it %v; amr-fleet-control GET /fleets/AMR1 %v\n",
				mark, before, fleetFacts(window), seen, fleet)
		}
	})
	f.eventually("the idle fleet (AVAILABLE 3 of 3) as the last capacity fact before the mark", 60*time.Second, func() bool {
		mark = kafkatest.EndOffset(f.t, mfcTopic)
		window = f.window(f.start, mark)
		before = lastFleet(window)
		return before["status"] == "AVAILABLE" && before["units_total"] == 3.0 && before["units_available"] == 3.0
	}, func() any { return fleetFacts(window) })
	f.fault("ROBOT_DISCONNECT", "R2", "", true)
	factsFrom(f.t, mark, func(evs []map[string]any) bool {
		seen = fleetFacts(evs)
		for _, d := range seen {
			if d["status"] == "DEGRADED" && d["units_total"] == 3.0 && d["units_available"] == 2.0 {
				return true
			}
		}
		return false
	}, 60*time.Second)
}
