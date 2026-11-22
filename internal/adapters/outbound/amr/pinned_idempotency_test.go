package amr

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"

	"github.com/IQVO/material-flow-control/internal/application/command"
)

// longestCommandKey is the longest Idempotency-Key the dispatcher mints (command.New's key: a leg ref
// "<uuid order id>.<seq>.<attempt>", prefixed per kind), with a generous two-digit seq and attempt.
func longestCommandKey() string {
	ref := uuid.NewString() + ".99.99"
	longest := ""
	for _, k := range []command.Kind{command.RequestLeg, command.CancelLeg, command.ConfirmPickup} {
		if key := command.New("c", "o", ref, k, time.Now(), time.Hour).Key; len(key) > len(longest) {
			longest = key
		}
	}
	return longest
}

// carry (Task 24 review): every POST this client sends declares the Idempotency-Key header parameter, required, and
// long enough for every key the dispatcher mints.
func TestEveryPostWeSendDeclaresTheIdempotencyKeyHeader(t *testing.T) {
	b, err := os.ReadFile("../../../../contracts/amr-fleet-control.openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	longest := longestCommandKey()
	for _, path := range []string{"/fleets/{fleetId}/legs", "/fleets/{fleetId}/legs/{legId}/cancellation"} {
		param := idempotencyParam(t, doc, path)
		if param["in"] != "header" || param["required"] != true {
			t.Fatalf("POST %s: Idempotency-Key = %v, want a required header", path, param)
		}
		if limit, ok := idemGet(param, "schema", "maxLength").(int); !ok || limit < len(longest) {
			t.Fatalf("POST %s: Idempotency-Key maxLength %v < %d, the length of our longest key %q", path, idemGet(param, "schema", "maxLength"), len(longest), longest)
		}
	}
}

// idempotencyParam resolves the POST's parameters (inline or local $ref) and returns the Idempotency-Key one.
func idempotencyParam(t *testing.T, doc map[string]any, path string) map[string]any {
	t.Helper()
	params, _ := idemGet(doc, "paths", path, "post", "parameters").([]any)
	for _, p := range params {
		if ref, ok := idemGet(p, "$ref").(string); ok {
			name, _ := strings.CutPrefix(ref, "#/components/parameters/")
			p = idemGet(doc, "components", "parameters", name)
		}
		if m, ok := p.(map[string]any); ok && m["name"] == "Idempotency-Key" {
			return m
		}
	}
	t.Fatalf("POST %s declares no Idempotency-Key parameter", path)
	return nil
}

func idemGet(m any, path ...string) any {
	for _, p := range path {
		mm, ok := m.(map[string]any)
		if !ok {
			return nil
		}
		m = mm[p]
	}
	return m
}
