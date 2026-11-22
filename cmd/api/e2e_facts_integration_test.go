//go:build integration

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/IQVO/material-flow-control/internal/testing/kafkatest"
)

// The fact helpers of the e2e test. ruling P4-R19: every read is POSITIONAL, from a mark (kafkatest.EndOffset) taken
// before its stimulus; nothing reads the topic from offset 0, so no older fact (the boot ramp, an earlier -count run)
// can satisfy a wait.

// factsFrom reads warehouse.material-flow-control.events from offset from (a mark) until holds (kafkatest dumps the
// keys read on a timeout).
// ruling P4-R19
func factsFrom(t *testing.T, from int64, until func([]map[string]any) bool, timeout time.Duration) []map[string]any {
	t.Helper()
	var out []map[string]any
	kafkatest.ReadFrom(t, mfcTopic, from, func(ms []kafkatest.Message) bool {
		out = decode(ms)
		return until(out)
	}, timeout)
	return out
}

// window reads this topic's facts at offsets [from, to): the facts before a mark.
func (f *floor) window(from, to int64) []map[string]any {
	f.t.Helper()
	if to <= from {
		return nil
	}
	return decode(kafkatest.ReadFrom(f.t, mfcTopic, from, func(ms []kafkatest.Message) bool {
		return len(ms) > 0 && ms[len(ms)-1].Offset >= to-1
	}, 30*time.Second))
}

func decode(ms []kafkatest.Message) []map[string]any {
	out := make([]map[string]any, 0, len(ms))
	for _, m := range ms {
		var ev map[string]any
		_ = json.Unmarshal(m.Value, &ev)
		out = append(out, ev)
	}
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

// outcomes are the TransportCompleted and TransportFailed facts of an order.
func outcomes(evs []map[string]any, orderID string) []map[string]any {
	return append(named(evs, orderID, "TransportCompleted"), named(evs, orderID, "TransportFailed")...)
}

// ended waits, from the order's mark, for the one outcome of an order and returns it.
func (f *floor) ended(orderID string, timeout time.Duration) map[string]any {
	f.t.Helper()
	mark, ok := f.marks[orderID]
	if !ok {
		f.t.Fatalf("order %s was not accepted by this run (no mark)", orderID)
	}
	evs := factsFrom(f.t, mark, func(evs []map[string]any) bool { return len(outcomes(evs, orderID)) > 0 }, timeout)
	if all := outcomes(evs, orderID); len(all) != 1 {
		f.t.Fatalf("order %s ended %d times: %v", orderID, len(all), all)
	}
	return outcomes(evs, orderID)[0]
}

// completed asserts the order ended with TransportCompleted.
func (f *floor) completed(what, orderID string, timeout time.Duration) {
	f.t.Helper()
	if ev := f.ended(orderID, timeout); !strings.HasSuffix(fmt.Sprint(ev["type"]), ".TransportCompleted") {
		f.t.Fatalf("%s (order %s) = %v", what, orderID, ev)
	}
}

func fleetFacts(evs []map[string]any) []map[string]any {
	var out []map[string]any
	for _, ev := range named(evs, "AMR_FLEET/AMR1", "SubsystemCapacityChanged") {
		out = append(out, data(ev))
	}
	return out
}

func lastFleet(evs []map[string]any) map[string]any {
	all := named(evs, "AMR_FLEET/AMR1", "SubsystemCapacityChanged")
	if len(all) == 0 {
		return map[string]any{"status": "", "units_available": 0.0}
	}
	return data(all[len(all)-1])
}

// caughtUp waits until material-flow-control has handled both subsystem topics up to marks taken now, and its view
// then has AMR_FLEET/AMR1 AVAILABLE 3 of 3 and ASRS/ASRS1 not UNAVAILABLE. ruling P4-R19: with -count=N its new
// consumer groups replay the earlier runs' facts from offset 0, their boot ramps (UNAVAILABLE) included, and a
// request made mid-replay was refused SUBSYSTEM_UNAVAILABLE.
func (f *floor) caughtUp(groups map[string]string) {
	f.t.Helper()
	lag := map[string][2]int64{}
	var view map[string]any
	f.eventually("material-flow-control caught up with both subsystems, AMR1 AVAILABLE 3 of 3", 60*time.Second, func() bool {
		behind := false
		for topic, group := range groups {
			end := kafkatest.EndOffset(f.t, topic)
			done := kafkatest.Committed(f.t, group, topic)
			lag[topic] = [2]int64{done, end}
			behind = behind || (end > 0 && done < end)
		}
		_, view = f.call(http.MethodGet, f.mfc+"/subsystems", nil, "")
		amr, asrs := subsystem(view, "AMR_FLEET", "AMR1"), subsystem(view, "ASRS", "ASRS1")
		return !behind && amr["status"] == "AVAILABLE" && amr["units_total"] == 3.0 && amr["units_available"] == 3.0 &&
			asrs["status"] != "UNAVAILABLE"
	}, func() any { return fmt.Sprintf("committed/end offsets %v; GET /subsystems %v", lag, view) })
}

func subsystem(view map[string]any, kind, id string) map[string]any {
	all, _ := view["subsystems"].([]any)
	for _, s := range all {
		if m, _ := s.(map[string]any); m["subsystem_kind"] == kind && m["subsystem_id"] == id {
			return m
		}
	}
	return map[string]any{}
}
