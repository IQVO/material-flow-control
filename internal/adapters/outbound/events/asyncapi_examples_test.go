package events_test

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/IQVO/material-flow-control/internal/adapters/outbound/events"
)

// carry (Task 17 review): each AsyncAPI example is what the encoder writes for the golden case of its type and
// subject: the same type and subject, the events-stream dataschema ending :<Name>:v1, and data equal field for field.
func TestEveryAsyncAPIExampleIsTheEncoderOutputOfItsGoldenCase(t *testing.T) {
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
	if len(doc.Components.Messages) != 7 {
		t.Fatalf("%d messages, want 7", len(doc.Components.Messages))
	}
	for name, m := range doc.Components.Messages {
		for _, ex := range m.Examples {
			p := normalize(t, ex.Payload)
			typ, subject := p["type"].(string), p["subject"].(string)
			if !strings.HasSuffix(typ, "."+name) || p["dataschema"] != "urn:warehouse:material-flow-control:events:"+name+":v1" {
				t.Fatalf("%s example: type %s dataschema %v", name, typ, p["dataschema"])
			}
			got := encoded(t, typ, subject)
			if got == nil {
				t.Fatalf("%s example (subject %s): no golden case with that type and subject", name, subject)
			}
			if !reflect.DeepEqual(p["data"], got) {
				t.Fatalf("%s example data =\n%v\nthe encoder writes\n%v", name, p["data"], got)
			}
		}
	}
}

// encoded is the data the sink writes for the golden case of typ and subject (nil when there is none).
func encoded(t *testing.T, typ, subject string) map[string]any {
	t.Helper()
	for _, c := range goldenCases() {
		if c.typ != typ || c.subject != subject {
			continue
		}
		out := &rows{}
		if err := events.NewSink(out, ids()).Append(context.Background(), c.ev); err != nil {
			t.Fatal(err)
		}
		return data(t, out.msgs[0])
	}
	return nil
}

// normalize round-trips v through JSON so YAML ints and JSON float64s compare equal.
func normalize(t *testing.T, v map[string]any) map[string]any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}
