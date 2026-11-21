package mcp_test

import (
	"strings"
	"testing"
)

// carry (Task 20 review): list_equipment_units over the real Reader answers asrs-not-found for an AS/RS the
// layout does not name, with the REST adapter's slug vocabulary.
func TestAnUnknownAsrsIsAsrsNotFound(t *testing.T) {
	h := newHarness(t)
	if text := errorText(h.call("list_equipment_units", `{"asrsId":"ASRS9"}`)); !strings.Contains(text, "asrs-not-found: AS/RS not found") {
		t.Fatalf("unknown AS/RS = %q", text)
	}
	var got struct {
		AsrsID string `json:"asrs_id"`
	}
	h.structured(h.call("list_equipment_units", `{"asrsId":"ASRS1"}`), &got)
	if got.AsrsID != "ASRS1" {
		t.Fatalf("a configured AS/RS = %+v", got)
	}
}
