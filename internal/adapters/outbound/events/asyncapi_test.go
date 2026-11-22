package events_test

import (
	"os"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestTheAsyncAPIContractDeclaresExactlyTheSevenTypesWithOneExampleEach(t *testing.T) {
	b, err := os.ReadFile("../../../../apis/asyncapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Components struct {
			Messages map[string]struct {
				Examples []struct {
					Payload map[string]any `yaml:"payload"`
				} `yaml:"examples"`
			} `yaml:"messages"`
		} `yaml:"components"`
	}
	if err := yaml.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	var names []string
	for name, m := range doc.Components.Messages {
		names = append(names, name)
		if len(m.Examples) != 1 || m.Examples[0].Payload["type"] == nil {
			t.Fatalf("%s: %d examples", name, len(m.Examples))
		}
	}
	sort.Strings(names)
	want := []string{"ContainerArrivedAtStation", "ContainerSentToClarification", "SubsystemCapacityChanged", "TransportAccepted",
		"TransportCompleted", "TransportFailed", "TransportRejected"}
	if len(names) != len(want) {
		t.Fatalf("messages = %v", names)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("messages = %v, want %v", names, want)
		}
	}
}

// carry (Task 7 review): the capacity consumer (wes-work-planning, plan 05b) must branch on subsystem_kind;
// the contract says which fields mean something for which kind and that counts never sum across kinds.
func TestTheCapacityContractTellsConsumersToBranchOnTheKind(t *testing.T) {
	b, err := os.ReadFile("../../../../apis/asyncapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Components struct {
			Messages map[string]struct {
				Description string `yaml:"description"`
			} `yaml:"messages"`
		} `yaml:"components"`
	}
	if err := yaml.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	d := doc.Components.Messages["SubsystemCapacityChanged"].Description
	for _, want := range []string{
		"branch on `subsystem_kind`",
		"`buffer_limit`, `occupied` and `queue_depth` are meaningful only for `STATION` (0 for `AMR_FLEET` and `ASRS`)",
		"never sum `units_available` or `open_work` across kinds",
		"`AMR_FLEET` available = idle and assignable robots",
		"`ASRS` available = units not HELD, which may be busy",
	} {
		if !strings.Contains(d, want) {
			t.Errorf("SubsystemCapacityChanged description lacks %q:\n%s", want, d)
		}
	}
}
