package telemetry

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/IQVO/material-flow-control/internal/application/ports"
)

// meterName scopes material-flow-control's own instruments.
const meterName = "github.com/IQVO/material-flow-control"

// transportsEndedName is a public contract with every dashboard built on it (ADR 0007): renaming it or its
// attributes needs a new ADR.
const transportsEndedName = "material_flow_control.transports.ended"

// TransportMetrics is the Tier 2 instrument set. A nil *TransportMetrics records nothing.
type TransportMetrics struct {
	ended metric.Int64Counter
}

var _ ports.TransportMetrics = (*TransportMetrics)(nil)

// NewTransportMetrics registers the counter on the global meter provider (installed by Setup; a no-op without it).
func NewTransportMetrics() (*TransportMetrics, error) {
	c, err := otel.Meter(meterName).Int64Counter(transportsEndedName, metric.WithUnit("{transport}"),
		metric.WithDescription("Accepted transports ended, by purpose, outcome (completed or failed) and failure reason"))
	if err != nil {
		return nil, err
	}
	return &TransportMetrics{ended: c}, nil
}

// TransportEnded counts one ending; took is the accepted-to-ended duration (kept for a later histogram).
func (m *TransportMetrics) TransportEnded(ctx context.Context, purpose, outcome, reason string, _ time.Duration) {
	if m == nil {
		return
	}
	m.ended.Add(ctx, 1, metric.WithAttributes(attribute.String("purpose", purpose), attribute.String("outcome", outcome),
		attribute.String("reason", reason)))
}
