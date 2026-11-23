//go:build integration

package main

import (
	"testing"
	"time"
)

// The plan 06 map-extension legs (decision D7): the four completing rows over the DEPLOYED map — amrMap() writes
// the pinned map UNCHANGED (its guard fails on any checkout that still lacks PK1/FWD1/CLR1), so every leg here
// routes over amr-fleet-control's own floor, the map of version 2026-11-22.1 built from the pinned commit 86a142a
// (CI's integration job checks it out; locally see docs/docs/development/testing.md). The rows are the legs that
// were refused 422 unknown-handoff-point before plan 06 (apply log 2026-11-22 item 12).
//
// ruling P4-R19: every wait reads only facts after its stimulus — accept() takes the topic's end offset BEFORE
// each POST and every fact helper reads from that mark; nothing reads the topic from offset 0.
//
// The wait budget fits `-timeout 25m` (the suite's CI step): container start <= 360 s, the catch-up 60 s, and
// the row waits <= 883 s (4 putaways 4 x (60 + 10), the two parking picks 2 x 60, replenishment 90 + 10, the two
// outbounds 2 x (90 + 10), clarification 90 + 30 + 30, the end check 3 + 30) — under the eight-row test's own
// 1040 s row budget, 21.1 min in all; a run on the emulator floor (SIM_SPEED=10) takes about two minutes.
func TestTheMapExtensionLegsAgainstTheRealSubsystems(t *testing.T) {
	f := startFloor(t)
	f.putaway("TOTE-20", "TOTE-21", "TOTE-22", "TOTE-23")
	// Row 1 — replenishment to the pick face: the AS/RS logical location -> PK1 (REPLENISH: source the AS/RS,
	// destination a handoff — the pack station's pick face).
	repl := f.accept(f.id("repl-pk1"), f.id("TOTE-20"), asrsLoc, "WH1-PCK-PK1", "REPLENISH")
	f.completed("replenishment to the pick face", repl, 90*time.Second)
	f.at(f.id("TOTE-20"), "AT_HANDOFF", "WH1-PCK-PK1")
	// Row 2 — outbound to the pick face: a tote parked at ST1 by a goods-to-person PICK, then ST1 -> PK1.
	f.completed("park TOTE-21 at ST1", f.accept(f.id("park-a"), f.id("TOTE-21"), asrsLoc, "ST1", "PICK"), 60*time.Second)
	outPK := f.accept(f.id("out-pk1"), f.id("TOTE-21"), "ST1", "WH1-PCK-PK1", "OUTBOUND")
	f.completed("outbound to the pick face", outPK, 90*time.Second)
	f.at(f.id("TOTE-21"), "AT_HANDOFF", "WH1-PCK-PK1")
	// Row 3 — outbound to the forward area: ST1 -> FWD1.
	f.completed("park TOTE-22 at ST1", f.accept(f.id("park-b"), f.id("TOTE-22"), asrsLoc, "ST1", "PICK"), 60*time.Second)
	outFWD := f.accept(f.id("out-fwd1"), f.id("TOTE-22"), "ST1", "WH1-FWD-FWD1", "OUTBOUND")
	f.completed("outbound to the forward area", outFWD, 90*time.Second)
	f.at(f.id("TOTE-22"), "AT_HANDOFF", "WH1-FWD-FWD1")
	// Row 4 — return to clarification over the repo floor: the deployed map itself serves CLR1 (the internal
	// MOVE of a NO_READ presentation lands AT_CLARIFICATION WH1-CLR-CLR1 with no inline map nodes anywhere).
	f.clarification("TOTE-23", "pick-clr")
	f.oneOutcomeEach(10, "(4 putaways, replenishment, 2 parking picks, 2 outbounds, clarification)")
}
