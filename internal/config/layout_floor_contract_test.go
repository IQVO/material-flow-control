package config_test

import (
	"os"
	"testing"

	"gopkg.in/yaml.v3"
)

// The example layout and the pinned amr map describe ONE floor (plan 06, decisions D5/D6): every handoffPointId
// the layout names — the AS/RS ports, the station, the handoffs and the clarification point — must be a
// handoffPoint of the pinned map contracts/amr-fleet-control.map.yaml (the byte copy of amr-fleet-control's
// config/map.example.yaml at the pin's pinned_commit; a pin is refreshed by copying, never edited, so this test
// reads the pin itself). SUBSET only, by design: the map carries amr's own nodes beyond the layout (ST2 is the
// decant handoff's map node), and the map↔world direction (map PORT ids == the emulator world's port nodeIds) is
// amr-fleet-control's floor contract test, not mfc's. What mfc owns is that nothing its layout asks amr to route
// to is unknown to the map (a leg to a missing point is refused 422 unknown-handoff-point).
func TestEveryExampleLayoutHandoffExistsOnThePinnedAMRMap(t *testing.T) {
	var layout struct {
		Asrs []struct {
			Ports []struct {
				HandoffPointID string `yaml:"handoffPointId"`
			} `yaml:"ports"`
		} `yaml:"asrs"`
		Stations []struct {
			HandoffPointID string `yaml:"handoffPointId"`
		} `yaml:"stations"`
		Handoffs []struct {
			HandoffPointID string `yaml:"handoffPointId"`
		} `yaml:"handoffs"`
		ClarificationPoints []struct {
			HandoffPointID string `yaml:"handoffPointId"`
		} `yaml:"clarificationPoints"`
	}
	unmarshalFile(t, "../../config/flow-layout.example.yaml", &layout)
	var ids []string
	for _, a := range layout.Asrs {
		for _, p := range a.Ports {
			ids = append(ids, p.HandoffPointID)
		}
	}
	for _, s := range layout.Stations {
		ids = append(ids, s.HandoffPointID)
	}
	for _, h := range layout.Handoffs {
		ids = append(ids, h.HandoffPointID)
	}
	for _, c := range layout.ClarificationPoints {
		ids = append(ids, c.HandoffPointID)
	}
	var m struct {
		Nodes []struct {
			HandoffPoint string `yaml:"handoffPoint"`
		} `yaml:"nodes"`
	}
	unmarshalFile(t, "../../contracts/amr-fleet-control.map.yaml", &m)
	handoffs := make(map[string]bool, len(m.Nodes))
	for _, n := range m.Nodes {
		if n.HandoffPoint != "" {
			handoffs[n.HandoffPoint] = true
		}
	}
	var missing []string
	for _, id := range ids {
		if !handoffs[id] {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("config/flow-layout.example.yaml names handoff point(s) %v that the pinned amr map contracts/amr-fleet-control.map.yaml does not carry: a transport leg to them is refused 422 unknown-handoff-point", missing)
	}
}

func unmarshalFile(t *testing.T, path string, into any) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(b, into); err != nil {
		t.Fatal(err)
	}
}
