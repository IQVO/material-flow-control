package http_test

import (
	"context"
	"net/http"
	"testing"
)

const expiredType = "https://errors.material-flow-control.warehouse-systems.dev/transport-request-expired"

// ruling P4-R22: a transport_request_id whose order the retention purged answers 410 transport-request-expired
// (FINAL) on GET, on a POST retry under a new key (never re-executed: no new order, no new event) and on a
// cancellation; the 410 under one key is stored and replayed; the same id with another request is still 409.
func TestAnExpiredTransportRequestAnswers410OnEveryRouteAndIsNeverReExecuted(t *testing.T) {
	a := newAPI(t)
	a.stored("TOTE-1")
	rec, body := a.do(http.MethodPost, "/transport-requests", "k-1", pick("task-1.1", "TOTE-1"))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("accept = %d %v", rec.Code, body)
	}
	a.store.PurgeOrder(body["transport_order_id"].(string))
	events := len(a.events.All())
	for name, call := range map[string]func() (int, map[string]any){
		"GET": func() (int, map[string]any) {
			r, b := a.do(http.MethodGet, "/transport-requests/task-1.1", "", nil)
			return r.Code, b
		},
		"POST": func() (int, map[string]any) {
			r, b := a.do(http.MethodPost, "/transport-requests", "k-2", pick("task-1.1", "TOTE-1"))
			return r.Code, b
		},
		"cancel": func() (int, map[string]any) {
			r, b := a.do(http.MethodPost, "/transport-requests/task-1.1/cancellation", "k-3", nil)
			return r.Code, b
		},
	} {
		if code, b := call(); code != http.StatusGone || b["type"] != expiredType || b["code"] != "transport-request-expired" {
			t.Errorf("%s of an expired id = %d %v, want 410 %s", name, code, b, expiredType)
		}
	}
	again, _ := a.do(http.MethodPost, "/transport-requests", "k-2", pick("task-1.1", "TOTE-1"))
	if again.Code != http.StatusGone || again.Header().Get("Idempotency-Replayed") != "true" {
		t.Errorf("the 410 under the same key = %d replayed=%q, want the stored FINAL answer", again.Code, again.Header().Get("Idempotency-Replayed"))
	}
	other := pick("task-1.1", "TOTE-1")
	other["priority"] = "NORMAL"
	if rec, b := a.do(http.MethodPost, "/transport-requests", "k-4", other); rec.Code != http.StatusConflict || b["code"] != "transport-request-conflict" {
		t.Errorf("the expired id with another request = %d %v, want 409 transport-request-conflict", rec.Code, b)
	}
	open, _ := a.orders.Open(context.Background())
	if len(open) != 0 || len(a.events.All()) != events {
		t.Fatalf("an expired retry re-executed: %d open orders, %d events (was %d)", len(open), len(a.events.All()), events)
	}
}
