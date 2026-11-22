package kafka

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// pinned loads a pinned AsyncAPI document of contracts/.
func pinned(t *testing.T, file string) map[string]any {
	t.Helper()
	b, err := os.ReadFile("../../../../contracts/" + file)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func get(m any, path ...string) any {
	for _, p := range path {
		mm, ok := m.(map[string]any)
		if !ok {
			return nil
		}
		m = mm[p]
	}
	return m
}

// examples lists every message example payload of the document.
func examples(doc map[string]any) []map[string]any {
	var out []map[string]any
	msgs, _ := get(doc, "components", "messages").(map[string]any)
	for _, m := range msgs {
		exs, _ := get(m, "examples").([]any)
		for _, ex := range exs {
			if p, ok := get(ex, "payload").(map[string]any); ok {
				out = append(out, p)
			}
		}
	}
	return out
}

// replay feeds every example of a handled type through the production handler and returns the handled types.
func replay(t *testing.T, doc map[string]any, handlers map[string]HandlerFunc) map[string]int {
	t.Helper()
	seen := map[string]int{}
	for _, p := range examples(doc) {
		typ, _ := p["type"].(string)
		if _, ok := handlers[typ]; !ok {
			continue
		}
		b, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		r := &recorder{}
		c := testConsumer(rebind(handlers, r))
		if err := c.HandleMessage(context.Background(), b); err != nil || r.count() != 1 {
			t.Fatalf("example of %s: err %v, applied %d", typ, err, r.count())
		}
		seen[typ]++
	}
	return seen
}

// dataSchema returns the `data` schema of one consumed event: inline (asrs-control) or a local
// `#/components/schemas/<Name>` $ref (amr-fleet-control).
// deviation (Task 24 D1): amr-fleet-control's pinned events reference their data schema instead of inlining it.
func dataSchema(t *testing.T, doc map[string]any, schema string) map[string]any {
	t.Helper()
	parts, _ := get(doc, "components", "schemas", schema, "allOf").([]any)
	for _, p := range parts {
		data, ok := get(p, "properties", "data").(map[string]any)
		if !ok {
			continue
		}
		if ref, ok := data["$ref"].(string); ok {
			name, found := strings.CutPrefix(ref, "#/components/schemas/")
			resolved, isMap := get(doc, "components", "schemas", name).(map[string]any)
			if !found || !isMap {
				t.Fatalf("schema %s: data $ref %q does not resolve", schema, ref)
			}
			data = resolved
		}
		if _, ok := data["properties"].(map[string]any); ok {
			return data
		}
	}
	t.Fatalf("schema %s has no data properties", schema)
	return nil
}

// dataFields lists the data property names of the schema of one consumed event.
func dataFields(t *testing.T, doc map[string]any, schema string) []string {
	t.Helper()
	props, _ := dataSchema(t, doc, schema)["properties"].(map[string]any)
	var out []string
	for k := range props {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// jsonFields lists the json names a payload struct decodes (nested structs by their own name only).
func jsonFields(v any) []string {
	var out []string
	rt := reflect.TypeOf(v)
	for i := 0; i < rt.NumField(); i++ {
		name, _, _ := strings.Cut(rt.Field(i).Tag.Get("json"), ",")
		out = append(out, name)
	}
	return out
}

func TestEveryPinnedAsrsExampleOfAConsumedTypeIsApplied(t *testing.T) {
	seen := replay(t, pinned(t, "asrs-control.asyncapi.yaml"), AsrsHandlers(&recorder{}))
	for _, typ := range []string{TypeContainerStored, TypeContainerPresented, TypeStorageJobFailed, TypeEquipmentStateChanged, TypeAsrsCapacityChanged} {
		if seen[typ] == 0 {
			t.Fatalf("the pinned contract has no example of %s", typ)
		}
	}
}

func TestEveryAsrsFieldWeDecodeExistsInThePinnedSchema(t *testing.T) {
	doc := pinned(t, "asrs-control.asyncapi.yaml")
	cases := map[string]any{"ContainerStoredEvent": containerStoredData{}, "ContainerPresentedAtPortEvent": containerPresentedData{},
		"StorageJobFailedEvent": storageJobFailedData{}, "EquipmentStateChangedEvent": equipmentStateData{}, "AsrsCapacityChangedEvent": asrsCapacityData{}}
	for schema, payload := range cases {
		have := map[string]bool{}
		for _, f := range dataFields(t, doc, schema) {
			have[f] = true
		}
		for _, f := range jsonFields(payload) {
			if !have[f] {
				t.Fatalf("%s: we decode %q, the producer's schema has no such field", schema, f)
			}
		}
	}
}
