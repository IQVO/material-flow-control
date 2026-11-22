package http_test

import (
	"net/http"
	"strings"
	"testing"
)

// carry (Task 18 review, FLEET ITEM): the fleet key rule — 1 to 128 printable ASCII characters (0x21-0x7E). A missing
// key is idempotency-key-required; any other key outside the rule is idempotency-key-invalid (400, FINAL), refused
// before the request runs; the longest and the widest valid keys run.
func TestAKeyOutsideTheFleetRuleIsIdempotencyKeyInvalid(t *testing.T) {
	f := newFixture(t)
	for _, key := range []string{strings.Repeat("k", 129), "two words", "k\tey", " lead", "trail ", "tötе", "k\x7f"} {
		rec, body := f.do(route, key, pick("task-9.1", "TOTE-9"))
		if rec.Code != http.StatusBadRequest || body["code"] != "idempotency-key-invalid" {
			t.Fatalf("key %q = %d %v, want 400 idempotency-key-invalid", key, rec.Code, body)
		}
	}
	if n := len(f.events.All()); n != 0 {
		t.Fatalf("events = %v, want none: an invalid key never runs the request", f.eventTypes())
	}
	for i, key := range []string{strings.Repeat("k", 128), "!", "~" + strings.Repeat("A", 126) + "!", "task-1.1:attempt/2#x"} {
		id := "task-" + string(rune('a'+i))
		if rec, body := f.do(route, key, pick(id, "TOTE-9")); rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("valid key %q = %d %v, want the request to run (422 rejection)", key, rec.Code, body)
		}
	}
}
