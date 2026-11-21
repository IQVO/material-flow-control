package events_test

import (
	"os"
	"sort"
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
