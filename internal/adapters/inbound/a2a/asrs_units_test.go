package a2a

import (
	"context"
	"testing"

	"github.com/IQVO/material-flow-control/internal/application/facts"
	"github.com/IQVO/material-flow-control/internal/application/usecases"
)

// unknownAsrsReads answers EquipmentUnits the way the Reader does for an AS/RS the layout does not name.
type unknownAsrsReads struct{ fakeReads }

func (unknownAsrsReads) EquipmentUnits(context.Context, string) ([]facts.EquipmentState, error) {
	return nil, usecases.ErrAsrsNotFound
}

// carry (Task 20 review): equipment_units answers the asrs-not-found problem (REST/MCP vocabulary), no data.
func TestAnUnknownAsrsIsAnAsrsNotFoundProblem(t *testing.T) {
	env, text := send(t, agent(t, unknownAsrsReads{}), skill("equipment_units", map[string]any{"asrsId": "ASRS9"}))
	p, _ := env["problem"].(map[string]any)
	if problemCode(env) != "asrs-not-found" || env["data"] != nil || p["status"] != float64(404) ||
		p["type"] != "https://errors.material-flow-control.warehouse-systems.dev/asrs-not-found" || text != "AS/RS not found (404): AS/RS not found" {
		t.Fatalf("envelope = %v %q", env, text)
	}
}
