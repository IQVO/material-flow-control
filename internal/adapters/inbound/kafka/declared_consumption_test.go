package kafka

import (
	"maps"
	"os"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The consumed types are declared in our own apis/asyncapi.yaml (the fleet's consumer-declaration rule: a producer's
// x-no-consumer-reason may be deleted only once the consumer's spec declares the type). The declaration must equal
// the dispatch tables exactly: a declared type nobody handles lies to the catalog, a handled type left undeclared
// shows the producer an orphan.
func TestOurAsyncAPIDeclaresExactlyTheTypesTheConsumersDispatch(t *testing.T) {
	b, err := os.ReadFile("../../../../apis/asyncapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	for topic, handlers := range map[string]map[string]HandlerFunc{AsrsTopic: AsrsHandlers(nil), AmrTopic: AmrHandlers(nil)} {
		key, declared := declaredTypes(t, doc, topic)
		if key == "" {
			t.Errorf("apis/asyncapi.yaml declares no channel with address %s", topic)
			continue
		}
		if want := slices.Sorted(maps.Keys(handlers)); !slices.Equal(declared, want) {
			t.Errorf("channel %s (%s) declares %v, the consumer dispatches %v", key, topic, declared, want)
		}
		if !receivesChannel(doc, key) {
			t.Errorf("no operation with action receive on channel %s (%s)", key, topic)
		}
	}
}

// declaredTypes returns the channel key whose address is topic and the sorted payload `type` consts of its messages.
func declaredTypes(t *testing.T, doc map[string]any, topic string) (string, []string) {
	t.Helper()
	channels, _ := doc["channels"].(map[string]any)
	for key, raw := range channels {
		ch, _ := raw.(map[string]any)
		if ch["address"] != topic {
			continue
		}
		msgs, _ := ch["messages"].(map[string]any)
		var types []string
		for name, m := range msgs {
			typ, ok := payloadTypeConst(doc, refd(doc, m)).(string)
			if !ok {
				t.Fatalf("channel %s message %s has no payload type const", key, name)
			}
			types = append(types, typ)
		}
		slices.Sort(types)
		return key, types
	}
	return "", nil
}

// payloadTypeConst finds properties.type.const in the message payload, directly or in one of its allOf parts.
func payloadTypeConst(doc, msg map[string]any) any {
	payload := refd(doc, msg["payload"])
	parts := []any{payload}
	if all, ok := payload["allOf"].([]any); ok {
		parts = append(parts, all...)
	}
	for _, p := range parts {
		props, _ := refd(doc, p)["properties"].(map[string]any)
		if typ, ok := props["type"].(map[string]any); ok && typ["const"] != nil {
			return typ["const"]
		}
	}
	return nil
}

// refd returns v as a map, following one local $ref (#/a/b/c) when v is a reference.
func refd(doc map[string]any, v any) map[string]any {
	m, _ := v.(map[string]any)
	ref, ok := m["$ref"].(string)
	if !ok {
		return m
	}
	var node any = doc
	for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		parent, _ := node.(map[string]any)
		node = parent[part]
	}
	out, _ := node.(map[string]any)
	return out
}

func receivesChannel(doc map[string]any, key string) bool {
	ops, _ := doc["operations"].(map[string]any)
	for _, raw := range ops {
		op, _ := raw.(map[string]any)
		ch, _ := op["channel"].(map[string]any)
		if op["action"] == "receive" && ch["$ref"] == "#/channels/"+key {
			return true
		}
	}
	return false
}
