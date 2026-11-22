// Package events turns material-flow-control's domain events into CloudEvents 1.0 outbox rows (ADR 0003):
// every event on BOTH warehouse.material-flow-control.events and warehouse.material-flow-control.analytics, under
// one id minted here and persisted with the rows.
package events

import (
	"context"
	"sort"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"

	"github.com/IQVO/material-flow-control/internal/adapters/kafka/cloudevents"
	"github.com/IQVO/material-flow-control/internal/application/outbox"
)

const (
	TopicEvents    = "warehouse.material-flow-control.events"
	TopicAnalytics = "warehouse.material-flow-control.analytics"
)

// Inserter is the outbox write side (postgres.OutboxRepo).
type Inserter interface {
	Insert(ctx context.Context, msgs ...outbox.Message) error
}

// Sink is the production ports.EventSink.
type Sink struct {
	out   Inserter
	newID func() string
}

func NewSink(out Inserter, newID func() string) *Sink { return &Sink{out: out, newID: newID} }

// occurrence is one event ready to encode.
type occurrence struct {
	entity, name, subject string
	at                    time.Time
	data                  any
}

// Append encodes every event first and writes nothing when one cannot be encoded.
func (s *Sink) Append(ctx context.Context, evs ...any) error {
	if len(evs) == 0 {
		return nil
	}
	msgs := make([]outbox.Message, 0, 2*len(evs))
	for _, e := range evs {
		o, err := describe(e)
		if err != nil {
			return err
		}
		id := s.newID()
		for _, stream := range []string{cloudevents.StreamEvents, cloudevents.StreamAnalytics} {
			m, err := encode(id, o, stream)
			if err != nil {
				return err
			}
			msgs = append(msgs, m)
		}
	}
	if tc := traceHeaders(ctx); len(tc) > 0 {
		for i := range msgs {
			msgs[i].Headers = append(msgs[i].Headers, tc...)
		}
	}
	return s.out.Insert(ctx, msgs...)
}

// traceHeaders is the W3C trace context (traceparent, tracestate, baggage) of the unit of work
// that appends the events, through the global propagator: the relay's publish span continues it
// (ADR 0007). With telemetry off the propagator is the no-op one and nothing is added, so the
// stored rows stay byte-identical to the goldens.
func traceHeaders(ctx context.Context) []outbox.Header {
	c := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, c)
	keys := c.Keys()
	sort.Strings(keys)
	out := make([]outbox.Header, 0, len(keys))
	for _, k := range keys {
		out = append(out, outbox.Header{Key: k, Value: c.Get(k)})
	}
	return out
}

func encode(id string, o occurrence, stream string) (outbox.Message, error) {
	value, err := cloudevents.New(cloudevents.Spec{ID: id, Entity: o.entity, EventName: o.name, Subject: o.subject,
		Time: o.at, Stream: stream, Version: 1, Data: o.data})
	if err != nil {
		return outbox.Message{}, err
	}
	topic := TopicEvents
	if stream == cloudevents.StreamAnalytics {
		topic = TopicAnalytics
	}
	h := cloudevents.ContentTypeHeader()
	return outbox.Message{EventID: id, Topic: topic, EventType: cloudevents.Type(o.entity, o.name), Subject: o.subject,
		Key: []byte(o.subject), DataSchema: cloudevents.DataSchema(stream, o.name, 1), Value: value,
		Headers: []outbox.Header{{Key: h.Key, Value: string(h.Value)}}}, nil
}
