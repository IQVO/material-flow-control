package kafka

import (
	"strings"
	"testing"
)

// channelTypes maps each CloudEvent type the pinned channel `topic` carries to the message component carrying it: every
// subscribe.message.oneOf $ref resolved to its message, the message's payload schema, and that schema's `type` const.
func channelTypes(t *testing.T, doc map[string]any, topic string) map[string]string {
	t.Helper()
	ch, ok := get(doc, "channels", topic).(map[string]any)
	if !ok {
		t.Fatalf("the pinned contract has no channel %s", topic)
	}
	refs, _ := get(ch, "subscribe", "message", "oneOf").([]any)
	if len(refs) == 0 {
		t.Fatalf("channel %s: no subscribe.message.oneOf", topic)
	}
	if desc, _ := ch["description"].(string); strings.Contains(desc, "Not an integration contract") {
		t.Fatalf("channel %s is not an integration contract: %s", topic, desc)
	}
	out := map[string]string{}
	for _, r := range refs {
		ref, _ := get(r, "$ref").(string)
		name, found := strings.CutPrefix(ref, "#/components/messages/")
		payload, _ := get(doc, "components", "messages", name, "payload", "$ref").(string)
		schema, isSchema := strings.CutPrefix(payload, "#/components/schemas/")
		if !found || !isSchema {
			t.Fatalf("channel %s: message ref %q / payload %q does not resolve", topic, ref, payload)
		}
		parts, _ := get(doc, "components", "schemas", schema, "allOf").([]any)
		for _, p := range parts {
			if c, ok := get(p, "properties", "type", "const").(string); ok {
				out[c] = name
			}
		}
	}
	return out
}

// carry (Task 24 review, E1): the topics we subscribe to are channels of the producers' pinned contracts, and every
// type we handle resolves through THAT channel's subscribe.message.oneOf (not merely somewhere in components).
func TestTheConsumedTopicsAreThePinnedChannelsOfTheHandledTypes(t *testing.T) {
	for _, c := range []struct {
		file, topic string
		handlers    map[string]HandlerFunc
	}{
		{"asrs-control.asyncapi.yaml", AsrsTopic, AsrsHandlers(&recorder{})},
		{"amr-fleet-control.asyncapi.yaml", AmrTopic, AmrHandlers(&recorder{})},
	} {
		carried := channelTypes(t, pinned(t, c.file), c.topic)
		if len(c.handlers) == 0 {
			t.Fatalf("%s: no handlers", c.topic)
		}
		for typ := range c.handlers {
			msg, ok := carried[typ]
			if !ok {
				t.Fatalf("we handle %s on %s, but the pinned channel carries %v", typ, c.topic, carried)
			}
			// The integration message is named after the type; an analytics twin (ContainerStoredAnalytics) is not.
			if last := typ[strings.LastIndex(typ, ".")+1:]; msg != last {
				t.Fatalf("%s on %s resolves through message %s, want the integration message %s", typ, c.topic, msg, last)
			}
		}
	}
}
