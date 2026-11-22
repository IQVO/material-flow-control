package amr

import (
	"os"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestTheLegRequestMatchesThePinnedOpenAPI(t *testing.T) {
	b, err := os.ReadFile("../../../../contracts/amr-fleet-control.openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	paths, _ := doc["paths"].(map[string]any)
	for _, p := range []string{"/fleets/{fleetId}/legs", "/fleets/{fleetId}/legs/{legId}/cancellation"} {
		if _, ok := paths[p]; !ok {
			t.Fatalf("pinned OpenAPI has no %s", p)
		}
	}
	schemas, _ := doc["components"].(map[string]any)["schemas"].(map[string]any)
	req, _ := schemas["LegRequest"].(map[string]any)
	props, _ := req["properties"].(map[string]any)
	rt := reflect.TypeOf(legRequest{})
	for i := 0; i < rt.NumField(); i++ {
		name, _, _ := strings.Cut(rt.Field(i).Tag.Get("json"), ",")
		if _, ok := props[name]; !ok {
			t.Fatalf("we send %q, LegRequest has no such property", name)
		}
	}
	required, _ := req["required"].([]any)
	for _, r := range required {
		found := false
		for i := 0; i < rt.NumField(); i++ {
			if n, _, _ := strings.Cut(rt.Field(i).Tag.Get("json"), ","); n == r {
				found = true
			}
		}
		if !found {
			t.Fatalf("LegRequest requires %v, we never send it", r)
		}
	}
}

// pinnedOpenAPI loads the pinned amr-fleet-control OpenAPI document.
func pinnedOpenAPI(t *testing.T) map[string]any {
	t.Helper()
	b, err := os.ReadFile("../../../../contracts/amr-fleet-control.openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

var backticked = regexp.MustCompile("`([a-z][a-z-]*)`")

// between lists the backticked problem codes of s between the first `from` and the next `to` ("" = end of s).
func between(t *testing.T, s, from, to string) []string {
	t.Helper()
	_, rest, ok := strings.Cut(s, from)
	if !ok {
		t.Fatalf("the pinned OpenAPI no longer says %q: re-read its replay classes", from)
	}
	if to != "" {
		rest, _, _ = strings.Cut(rest, to)
	}
	var out []string
	for _, m := range backticked.FindAllStringSubmatch(rest, -1) {
		out = append(out, m[1])
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// The producer documents its replay classes in info.description (P3-R13): the RETRYABLE codes release the key and
// are worth a retry under the same key, the FINAL ones are stored. amr.Retryable must be exactly the RETRYABLE set.
func TestRetryableIsExactlyThePinnedRetryableRefusals(t *testing.T) {
	desc, _ := pinnedOpenAPI(t)["info"].(map[string]any)["description"].(string)
	retryable := between(t, desc, "RETRYABLE refusals release the key", "\n\n")
	final := between(t, desc, "FINAL refusals are stored", "RETRYABLE refusals")
	if len(retryable) < 8 || len(final) < 3 {
		t.Fatalf("read %v RETRYABLE and %v FINAL codes from the pinned OpenAPI: the description changed shape", retryable, final)
	}
	ours := slices.Sorted(slices.Values(Retryable))
	if !slices.Equal(ours, retryable) {
		t.Fatalf("amr.Retryable = %v, the pinned contract's RETRYABLE refusals = %v", ours, retryable)
	}
	for _, code := range final {
		if slices.Contains(Retryable, code) {
			t.Fatalf("%s is FINAL in the pinned contract but in amr.Retryable", code)
		}
	}
}

// deviation (Task 24 D2): the answer side of the brief's request check. accepted() reads leg_id (required) and
// status from the Leg every 2xx of both calls carries.
func TestTheLegAnswerWeDecodeMatchesThePinnedOpenAPI(t *testing.T) {
	doc := pinnedOpenAPI(t)
	legSchema, _ := get(doc, "components", "schemas", "Leg").(map[string]any)
	props, _ := legSchema["properties"].(map[string]any)
	rt := reflect.TypeOf(leg{})
	for i := 0; i < rt.NumField(); i++ {
		name, _, _ := strings.Cut(rt.Field(i).Tag.Get("json"), ",")
		if _, ok := props[name]; !ok {
			t.Fatalf("we decode %q, Leg has no such property", name)
		}
	}
	if required, _ := legSchema["required"].([]any); !slices.Contains(required, any("leg_id")) {
		t.Fatalf("Leg.required = %v: accepted() treats a 2xx without leg_id as unreachable", required)
	}
	for _, c := range []struct{ path, status string }{{"/fleets/{fleetId}/legs", "202"},
		{"/fleets/{fleetId}/legs/{legId}/cancellation", "200"}, {"/fleets/{fleetId}/legs/{legId}/cancellation", "202"}} {
		ref := get(doc, "paths", c.path, "post", "responses", c.status, "content", "application/json", "schema", "$ref")
		if ref != "#/components/schemas/Leg" {
			t.Fatalf("POST %s %s answers %v, want a Leg", c.path, c.status, ref)
		}
	}
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
