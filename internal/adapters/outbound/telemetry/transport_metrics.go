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

// Instrument names are a public contract with every dashboard built on them (ADR 0007): renaming one
// or its attributes needs a new ADR.
const (
	transportsEndedName = "material_flow_control.transports.ended"
	outboxBacklogName   = "material_flow_control.outbox.backlog"
	outboxLagName       = "material_flow_control.outbox.lag"
)

// outboxReadTimeout bounds one backlog query of a metric collection.
const outboxReadTimeout = 2 * time.Second

// OutboxBacklogReader reads the unpublished outbox rows and the age of the oldest one
// (postgres.OutboxRepo.Backlog).
type OutboxBacklogReader func(ctx context.Context) (rows int64, oldest time.Duration, err error)

// TransportMetrics is the Tier 2 instrument set: the transports-ended counter and the outbox
// backlog gauges. A nil *TransportMetrics records nothing.
type TransportMetrics struct {
	meter metric.Meter
	ended metric.Int64Counter
}

var _ ports.TransportMetrics = (*TransportMetrics)(nil)

// NewTransportMetrics registers the counter on the global meter provider (installed by Setup; a no-op without it).
func NewTransportMetrics() (*TransportMetrics, error) {
	meter := otel.Meter(meterName)
	c, err := meter.Int64Counter(transportsEndedName, metric.WithUnit("{transport}"),
		metric.WithDescription("Accepted transports ended, by purpose, outcome (completed or failed) and failure reason"))
	if err != nil {
		return nil, err
	}
	return &TransportMetrics{meter: meter, ended: c}, nil
}

// TransportEnded counts one ending; took is the accepted-to-ended duration (kept for a later histogram).
func (m *TransportMetrics) TransportEnded(ctx context.Context, purpose, outcome, reason string, _ time.Duration) {
	if m == nil {
		return
	}
	m.ended.Add(ctx, 1, metric.WithAttributes(attribute.String("purpose", purpose), attribute.String("outcome", outcome),
		attribute.String("reason", reason)))
}

// ObserveOutbox registers the outbox gauges (ruling P4-R15, asrs-control parity):
// material_flow_control.outbox.backlog (unpublished rows, attempt-capped ones included) and
// material_flow_control.outbox.lag (seconds since the oldest unpublished row was written; 0 when the
// outbox is drained). read runs once per collection; a failed read observes nothing, so a database
// outage shows as a gap, never as a zero backlog.
func (m *TransportMetrics) ObserveOutbox(read OutboxBacklogReader) (metric.Registration, error) {
	backlog, err := m.meter.Int64ObservableGauge(outboxBacklogName, metric.WithUnit("{row}"),
		metric.WithDescription("Unpublished transactional-outbox rows (rows the relay excluded at the attempt cap included)."))
	if err != nil {
		return nil, err
	}
	lag, err := m.meter.Float64ObservableGauge(outboxLagName, metric.WithUnit("s"),
		metric.WithDescription("Age of the oldest unpublished transactional-outbox row; 0 when the outbox is drained."))
	if err != nil {
		return nil, err
	}
	return m.meter.RegisterCallback(func(ctx context.Context, o metric.Observer) error {
		rctx, cancel := context.WithTimeout(ctx, outboxReadTimeout)
		defer cancel()
		rows, oldest, err := read(rctx)
		if err != nil {
			return nil //nolint:nilerr // a failed read is a gap in the series, not an SDK error
		}
		o.ObserveInt64(backlog, rows)
		o.ObserveFloat64(lag, oldest.Seconds())
		return nil
	}, backlog, lag)
}
