package http_test

import (
	"net/http"
	"testing"
)

// ruling P4-R12: transport_request_id is caller-minted, so a cancellation that raced ahead of its request must
// not pin a 404 for 24 h — the key is released and the retry under the same key cancels the transport.
func TestACancellationBeforeItsRequestIsNotPinned(t *testing.T) {
	f := newFixture(t)
	f.stored("TOTE-1")
	first, body := f.do(route+"/task-5.1/cancellation", "k-cancel", nil)
	if first.Code != http.StatusNotFound || body["code"] != "transport-request-not-found" {
		t.Fatalf("first cancel = %d %v, want 404 transport-request-not-found", first.Code, body)
	}
	if got := f.idem.Rows("k-cancel"); got != 0 {
		t.Fatalf("key rows after the 404 = %d, want 0: the key must be released", got)
	}
	if rec, body := f.do(route, "k-req", pick("task-5.1", "TOTE-1")); rec.Code != http.StatusAccepted {
		t.Fatalf("request = %d %v", rec.Code, body)
	}
	retry, body := f.do(route+"/task-5.1/cancellation", "k-cancel", nil)
	if replayed(retry) || (retry.Code != http.StatusAccepted && retry.Code != http.StatusOK) {
		t.Fatalf("retry = %d replayed=%v %v, want a fresh 200/202", retry.Code, replayed(retry), body)
	}
	if _, view := f.do2(http.MethodGet, route+"/task-5.1"); view["status"] != "CANCEL_REQUESTED" && view["status"] != "CANCELLED" {
		t.Fatalf("transport = %v, want cancelled or cancel requested", view)
	}
	if got := f.idem.Rows("k-cancel"); got != 1 {
		t.Fatalf("key rows after the 2xx = %d, want 1", got)
	}
}

// ruling P4-R12: a container track is created by ContainerStored or an acceptance, so a resolution for a track
// not known yet releases its key: the retry under the same key executes again (here: not-in-clarification).
func TestAResolutionBeforeItsTrackExistsIsNotPinned(t *testing.T) {
	f := newFixture(t)
	path := "/container-tracks/TOTE-7/clarification-resolution"
	first, body := f.do(path, "k-res", map[string]any{"location": "X"})
	if first.Code != http.StatusNotFound || body["code"] != "container-track-not-found" {
		t.Fatalf("first resolution = %d %v, want 404 container-track-not-found", first.Code, body)
	}
	if got := f.idem.Rows("k-res"); got != 0 {
		t.Fatalf("key rows after the 404 = %d, want 0: the key must be released", got)
	}
	f.stored("TOTE-7")
	retry, body := f.do(path, "k-res", map[string]any{"location": "X"})
	if replayed(retry) || retry.Code == http.StatusNotFound {
		t.Fatalf("retry = %d replayed=%v %v, want a fresh execution against the now-known track", retry.Code, replayed(retry), body)
	}
}
