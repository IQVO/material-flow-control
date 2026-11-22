// Package asrs is the asrs-control REST client (relationship 5), against apis/openapi.yaml at 007bf7b.
package asrs

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/IQVO/material-flow-control/internal/adapters/outbound/subsystemhttp"
	"github.com/IQVO/material-flow-control/internal/application/ports"
	"github.com/IQVO/material-flow-control/internal/application/subsystem"
)

// Retryable lists asrs-control's RETRYABLE refusals ("RETRYABLE refusals release the key, so a retry under the
// same key executes again"); every other problem code is FINAL.
var Retryable = []string{"unknown-container", "container-busy", "container-already-stored", "storage-full",
	"aisle-unavailable", "port-unreachable", "port-not-presenting",
	// carry (Task 8 review): the two cancellation refusals are RETRYABLE in the openapi and errors.go (409).
	"job-not-cancellable", "job-ended"}

// Client calls asrs-control.
type Client struct{ c *subsystemhttp.Client }

// New builds a client for baseURL (ASRS_CONTROL_URL).
func New(baseURL string, timeout time.Duration) *Client {
	return &Client{c: subsystemhttp.New(baseURL, timeout, Retryable)}
}

var _ ports.AsrsClient = (*Client)(nil)

type storageJobRequest struct {
	Kind        string `json:"kind"`
	ContainerID string `json:"container_id"`
	PortID      string `json:"port_id,omitempty"`
	Reference   string `json:"reference"`
}

type storageJob struct {
	StorageJobID string `json:"storage_job_id"`
	Status       string `json:"status"`
}

type pickupRequest struct {
	ContainerID string `json:"container_id"`
}

type asrsView struct {
	RecommendedInputPortID *string `json:"recommended_input_port_id"`
}

func (a *Client) CreateStorageJob(ctx context.Context, key, asrsID string, r subsystem.StorageJobRequest) (subsystem.Accepted, error) {
	var out storageJob
	path := subsystemhttp.Path("asrs", asrsID, "storage-jobs")
	err := a.c.Do(ctx, http.MethodPost, path, key,
		storageJobRequest{Kind: r.Kind, ContainerID: r.ContainerID, PortID: r.PortID, Reference: r.Reference}, &out)
	return accepted(path, out, err)
}

func (a *Client) CancelStorageJob(ctx context.Context, key, asrsID, jobID string) (subsystem.Accepted, error) {
	var out storageJob
	path := subsystemhttp.Path("asrs", asrsID, "storage-jobs", jobID, "cancellation")
	err := a.c.Do(ctx, http.MethodPost, path, key, nil, &out)
	return accepted(path, out, err)
}

// accepted requires the storage_job_id every StorageJob answer carries.
// carry (Task 14 review): a 2xx without it is ErrUnreachable, never an acceptance with an empty id.
func accepted(path string, out storageJob, err error) (subsystem.Accepted, error) {
	if err != nil {
		return subsystem.Accepted{}, err
	}
	if out.StorageJobID == "" {
		return subsystem.Accepted{}, fmt.Errorf("%w: %s answered no id", subsystem.ErrUnreachable, path)
	}
	return subsystem.Accepted{ID: out.StorageJobID, Status: out.Status}, nil
}

func (a *Client) ConfirmPickup(ctx context.Context, key, asrsID, portID, containerID string) error {
	return a.c.Do(ctx, http.MethodPost, subsystemhttp.Path("asrs", asrsID, "ports", portID, "pickups"), key,
		pickupRequest{ContainerID: containerID}, nil)
}

func (a *Client) RecommendedInputPort(ctx context.Context, asrsID string) (string, error) {
	var out asrsView
	if err := a.c.Do(ctx, http.MethodGet, subsystemhttp.Path("asrs", asrsID), "", nil, &out); err != nil {
		return "", err
	}
	if out.RecommendedInputPortID == nil {
		return "", nil
	}
	return *out.RecommendedInputPortID, nil
}
