// Package amr is the amr-fleet-control REST client (relationship 4), against plan 03's interface table until
// Task 24 pins its built apis/openapi.yaml.
package amr

import (
	"context"
	"net/http"
	"time"

	"github.com/IQVO/material-flow-control/internal/adapters/outbound/subsystemhttp"
	"github.com/IQVO/material-flow-control/internal/application/ports"
	"github.com/IQVO/material-flow-control/internal/application/subsystem"
)

// Retryable lists amr-fleet-control's RETRYABLE refusals (plan 03 relationship-4 table, R column).
var Retryable = []string{"handoff-unreachable", "container-busy", "fleet-unavailable"}

// Client calls amr-fleet-control.
type Client struct{ c *subsystemhttp.Client }

// New builds a client for baseURL (AMR_FLEET_CONTROL_URL).
func New(baseURL string, timeout time.Duration) *Client {
	return &Client{c: subsystemhttp.New(baseURL, timeout, Retryable)}
}

var _ ports.AmrClient = (*Client)(nil)

type legRequest struct {
	ContainerID string `json:"container_id"`
	From        string `json:"from_handoff_point_id"`
	To          string `json:"to_handoff_point_id"`
	Reference   string `json:"reference"`
}

type leg struct {
	LegID  string `json:"leg_id"`
	Status string `json:"status"`
}

func (a *Client) RequestLeg(ctx context.Context, key, fleetID string, r subsystem.LegRequest) (subsystem.Accepted, error) {
	var out leg
	err := a.c.Do(ctx, http.MethodPost, subsystemhttp.Path("fleets", fleetID, "legs"), key,
		legRequest{ContainerID: r.ContainerID, From: r.From, To: r.To, Reference: r.Reference}, &out)
	return subsystem.Accepted{ID: out.LegID, Status: out.Status}, err
}

func (a *Client) CancelLeg(ctx context.Context, key, fleetID, legID string) (subsystem.Accepted, error) {
	var out leg
	err := a.c.Do(ctx, http.MethodPost, subsystemhttp.Path("fleets", fleetID, "legs", legID, "cancellation"), key, nil, &out)
	return subsystem.Accepted{ID: out.LegID, Status: out.Status}, err
}
