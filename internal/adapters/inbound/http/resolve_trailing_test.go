package http_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// carry (Task 18 review): the resolution body is ONE JSON value, like every other body (decodeJSON's dec.More()):
// trailing data after it is 400 bad-request (FINAL).
func TestAResolutionBodyWithTrailingDataIsABadRequest(t *testing.T) {
	a := newAPI(t)
	for i, raw := range []string{`{"location":"WH1-CLR-CLR1"} {}`, `{} []`, `{"location":"WH1-CLR-CLR1"}{"location":"ST1"}`} {
		req := httptest.NewRequest(http.MethodPost, "/container-tracks/TOTE-1/clarification-resolution", strings.NewReader(raw))
		req.Header.Set("Idempotency-Key", "k-trailing-"+string(rune('a'+i)))
		rec := httptest.NewRecorder()
		a.h.ServeHTTP(rec, req)
		var body map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if rec.Code != http.StatusBadRequest || body["code"] != "bad-request" {
			t.Fatalf("resolution body %q = %d %v, want 400 bad-request", raw, rec.Code, body)
		}
	}
}
