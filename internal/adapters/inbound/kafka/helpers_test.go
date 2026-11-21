package kafka

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sync"

	kafkago "github.com/segmentio/kafka-go"

	"github.com/IQVO/material-flow-control/internal/application/facts"
)

// recorder is the Facts double: it records every applied fact and can fail the first N calls.
type recorder struct {
	mu       sync.Mutex
	failures int
	calls    []any
	ids      []string
}

var errTransient = errors.New("transient")

func (r *recorder) apply(id string, f any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failures > 0 {
		r.failures--
		return errTransient
	}
	r.calls, r.ids = append(r.calls, f), append(r.ids, id)
	return nil
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func (r *recorder) OnContainerStored(_ context.Context, id, _ string, f facts.ContainerStored) error {
	return r.apply(id, f)
}

func (r *recorder) OnContainerPresented(_ context.Context, id, _ string, f facts.ContainerPresented) error {
	return r.apply(id, f)
}

func (r *recorder) OnStorageJobFailed(_ context.Context, id, _ string, f facts.StorageJobFailed) error {
	return r.apply(id, f)
}

func (r *recorder) OnEquipmentStateChanged(_ context.Context, id, _ string, f facts.EquipmentState) error {
	return r.apply(id, f)
}

func (r *recorder) OnAsrsCapacityChanged(_ context.Context, id, _ string, f facts.AsrsCapacity) error {
	return r.apply(id, f)
}

func (r *recorder) OnLegAssigned(_ context.Context, id, _ string, f facts.LegAssigned) error {
	return r.apply(id, f)
}

func (r *recorder) OnContainerPickedUp(_ context.Context, id, _ string, f facts.ContainerPickedUp) error {
	return r.apply(id, f)
}

func (r *recorder) OnLegCompleted(_ context.Context, id, _ string, f facts.LegCompleted) error {
	return r.apply(id, f)
}

func (r *recorder) OnLegFailed(_ context.Context, id, _ string, f facts.LegFailed) error {
	return r.apply(id, f)
}

func (r *recorder) OnFleetCapacityChanged(_ context.Context, id, _ string, f facts.FleetCapacity) error {
	return r.apply(id, f)
}

// event is one structured-mode CloudEvent with data.
func event(id, typ string, data map[string]any) []byte {
	b, _ := json.Marshal(map[string]any{"specversion": "1.0", "id": id, "source": "/warehouse/test", "type": typ, "subject": "s",
		"time": "2026-11-20T08:00:00Z", "datacontenttype": "application/json", "dataschema": "urn:test", "data": data})
	return b
}

type dlq struct {
	mu   sync.Mutex
	msgs []kafkago.Message
}

func (d *dlq) WriteMessages(_ context.Context, msgs ...kafkago.Message) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.msgs = append(d.msgs, msgs...)
	return nil
}

func (d *dlq) Close() error { return nil }

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func testConsumer(handlers map[string]HandlerFunc) *Consumer {
	return &Consumer{Name: "test", Topic: "t", Handlers: handlers, Logger: quiet(), DLQ: &dlq{}}
}
