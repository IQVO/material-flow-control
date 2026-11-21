package asrs_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/IQVO/material-flow-control/internal/adapters/outbound/asrs"
	"github.com/IQVO/material-flow-control/internal/application/subsystem"
)

type call struct {
	Method, Path, Key string
	Body              map[string]any
}

func fake(t *testing.T, status int, answer string) (*asrs.Client, *[]call) {
	t.Helper()
	var calls []call
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		c := call{Method: r.Method, Path: r.URL.Path, Key: r.Header.Get("Idempotency-Key")}
		_ = json.Unmarshal(b, &c.Body)
		calls = append(calls, c)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, answer)
	}))
	t.Cleanup(srv.Close)
	return asrs.New(srv.URL, time.Second), &calls
}

func TestARetrieveOmitsThePortAndAStoreNamesIt(t *testing.T) {
	c, calls := fake(t, http.StatusAccepted, `{"storage_job_id":"sj-1","asrs_id":"ASRS1","kind":"RETRIEVE","status":"QUEUED"}`)
	acc, err := c.CreateStorageJob(context.Background(), "to-1.1.1", "ASRS1", subsystem.StorageJobRequest{Kind: "RETRIEVE", ContainerID: "TOTE-1", Reference: "to-1.1.1"})
	if err != nil || acc != (subsystem.Accepted{ID: "sj-1", Status: "QUEUED"}) {
		t.Fatalf("accepted = %+v %v", acc, err)
	}
	_, _ = c.CreateStorageJob(context.Background(), "to-2.2.1", "ASRS1", subsystem.StorageJobRequest{Kind: "STORE", ContainerID: "TOTE-2", PortID: "IN2", Reference: "to-2.2.1"})
	got := *calls
	if got[0].Method != "POST" || got[0].Path != "/asrs/ASRS1/storage-jobs" || got[0].Key != "to-1.1.1" {
		t.Fatalf("retrieve call = %+v", got[0])
	}
	if _, has := got[0].Body["port_id"]; has || got[0].Body["kind"] != "RETRIEVE" || got[0].Body["container_id"] != "TOTE-1" || got[0].Body["reference"] != "to-1.1.1" {
		t.Fatalf("retrieve body = %+v", got[0].Body)
	}
	if got[1].Body["port_id"] != "IN2" || got[1].Body["kind"] != "STORE" {
		t.Fatalf("store body = %+v", got[1].Body)
	}
}

func TestCancelAndPickupPaths(t *testing.T) {
	c, calls := fake(t, http.StatusOK, `{"storage_job_id":"sj-1","status":"CANCELLED"}`)
	if acc, err := c.CancelStorageJob(context.Background(), "cancel-r", "ASRS1", "sj-1"); err != nil || acc.Status != "CANCELLED" {
		t.Fatalf("cancel = %+v %v", acc, err)
	}
	if err := c.ConfirmPickup(context.Background(), "pickup-r", "ASRS1", "OUT1", "TOTE-1"); err != nil {
		t.Fatal(err)
	}
	got := *calls
	if got[0].Path != "/asrs/ASRS1/storage-jobs/sj-1/cancellation" || got[0].Key != "cancel-r" || got[0].Body != nil {
		t.Fatalf("cancel call = %+v", got[0])
	}
	if got[1].Path != "/asrs/ASRS1/ports/OUT1/pickups" || got[1].Key != "pickup-r" || got[1].Body["container_id"] != "TOTE-1" {
		t.Fatalf("pickup call = %+v", got[1])
	}
}

func TestTheRecommendedInputPortMayBeNull(t *testing.T) {
	c, calls := fake(t, http.StatusOK, `{"asrs_id":"ASRS1","recommended_input_port_id":"IN2"}`)
	if id, err := c.RecommendedInputPort(context.Background(), "ASRS1"); err != nil || id != "IN2" || (*calls)[0].Method != "GET" || (*calls)[0].Path != "/asrs/ASRS1" {
		t.Fatalf("recommended = %q %v %+v", id, err, *calls)
	}
	n, _ := fake(t, http.StatusOK, `{"asrs_id":"ASRS1","recommended_input_port_id":null}`)
	if id, err := n.RecommendedInputPort(context.Background(), "ASRS1"); err != nil || id != "" {
		t.Fatalf("null = %q %v", id, err)
	}
}

func TestAsrsRetryableCodesAreTheDocumentedOnes(t *testing.T) {
	for _, code := range []string{"unknown-container", "container-busy", "container-already-stored", "storage-full", "aisle-unavailable", "port-unreachable", "port-not-presenting"} {
		c, _ := fake(t, http.StatusUnprocessableEntity, `{"status":422,"code":"`+code+`"}`)
		_, err := c.CreateStorageJob(context.Background(), "k", "ASRS1", subsystem.StorageJobRequest{Kind: "RETRIEVE", ContainerID: "C", Reference: "r"})
		var r *subsystem.Refusal
		if !errors.As(err, &r) || !r.Retryable {
			t.Fatalf("%s = %v", code, err)
		}
	}
	for _, code := range []string{"bad-request", "invalid-parameter", "asrs-not-found", "unknown-port", "wrong-port-kind", "idempotency-key-reused"} {
		c, _ := fake(t, http.StatusBadRequest, `{"status":400,"code":"`+code+`"}`)
		_, err := c.CreateStorageJob(context.Background(), "k", "ASRS1", subsystem.StorageJobRequest{Kind: "RETRIEVE", ContainerID: "C", Reference: "r"})
		var r *subsystem.Refusal
		if !errors.As(err, &r) || r.Retryable {
			t.Fatalf("%s = %v", code, err)
		}
	}
}
