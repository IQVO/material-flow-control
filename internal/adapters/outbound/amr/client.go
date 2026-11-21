// Package amr is the amr-fleet-control REST client (relationship 4), against plan 03's interface table until
// Task 24 pins its built apis/openapi.yaml.
package amr

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/IQVO/material-flow-control/internal/adapters/outbound/subsystemhttp"
	"github.com/IQVO/material-flow-control/internal/application/ports"
	"github.com/IQVO/material-flow-control/internal/application/subsystem"
)

// Retryable lists amr-fleet-control's RETRYABLE refusals (plan 03 relationship-4 table, R column).
var Retryable = []string{"handoff-unreachable", "container-busy", "fleet-unavailable",
	// carry (Task 8 review): the two cancellation refusals are 409 R in the relationship-4 table.
	"leg-not-cancellable", "leg-ended"}

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
	path := subsystemhttp.Path("fleets", fleetID, "legs")
	err := a.c.Do(ctx, http.MethodPost, path, key,
		legRequest{ContainerID: r.ContainerID, From: r.From, To: r.To, Reference: r.Reference}, &out)
	return accepted(path, out, err)
}

func (a *Client) CancelLeg(ctx context.Context, key, fleetID, legID string) (subsystem.Accepted, error) {
	var out leg
	path := subsystemhttp.Path("fleets", fleetID, "legs", legID, "cancellation")
	err := a.c.Do(ctx, http.MethodPost, path, key, nil, &out)
	return accepted(path, out, err)
}

// accepted requires the leg_id every Leg answer carries.
// carry (Task 14 review): a 2xx without it is ErrUnreachable, never an acceptance with an empty id.
func accepted(path string, out leg, err error) (subsystem.Accepted, error) {
	if err != nil {
		return subsystem.Accepted{}, err
	}
	if out.LegID == "" {
		return subsystem.Accepted{}, fmt.Errorf("%w: %s answered no id", subsystem.ErrUnreachable, path)
	}
	return subsystem.Accepted{ID: out.LegID, Status: out.Status}, nil
}
