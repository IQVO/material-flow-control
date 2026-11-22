package kafka

// carry (Task 15 review): the Consumer-level shapes of the ported customer-returns loop tests: a poison message's
// dead-letter publish is retried BEFORE its offset is committed, and a shutdown while a message is being handled
// neither dead-letters nor commits it.

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	kafkago "github.com/segmentio/kafka-go"

	"github.com/IQVO/material-flow-control/internal/application/facts"
)

// flakyDLQ fails the first `failures` writes, recording every attempt in rec.
type flakyDLQ struct {
	mu       sync.Mutex
	rec      *loopRecorder
	failures int
	written  []kafkago.Message
}

func (d *flakyDLQ) WriteMessages(_ context.Context, msgs ...kafkago.Message) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.failures > 0 {
		d.failures--
		d.rec.add("dlq:fail")
		return errors.New("dlq broker blip")
	}
	d.rec.add("dlq:ok")
	d.written = append(d.written, msgs...)
	return nil
}

func (d *flakyDLQ) Close() error { return nil }

func TestAPoisonMessageIsDeadLetteredWithRetriesBeforeItsOffsetIsCommitted(t *testing.T) {
	rec := &loopRecorder{}
	poison := event("e-poison", TypeContainerStored, map[string]any{"storage_job_id": "sj-1"})
	reader := &scriptedReader{rec: rec, msgs: []kafkago.Message{{Topic: AsrsTopic, Offset: 0, Key: []byte("sj-1"), Value: poison}}}
	w := &flakyDLQ{rec: rec, failures: 2}
	r := &recorder{}
	c := &Consumer{Name: "test", Topic: AsrsTopic, Reader: reader, Handlers: AsrsHandlers(r), Logger: quiet(), DLQ: w,
		sleep: func(_ context.Context, d time.Duration) error { rec.add("sleep:%s", d); return nil }}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	deadline := time.Now().Add(5 * time.Second)
	for !slices.Contains(rec.log(), "commit:0") && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("run = %v", err)
	}
	want := []string{"fetch:0", "dlq:fail", "sleep:200ms", "dlq:fail", "sleep:400ms", "dlq:ok", "commit:0"}
	if got := rec.log(); !reflect.DeepEqual(got, want) {
		t.Fatalf("event order\n got  %v\n want %v", got, want)
	}
	if r.count() != 0 || len(w.written) != 1 || w.written[0].Topic != AsrsTopic+DLQSuffix || string(w.written[0].Value) != string(poison) {
		t.Fatalf("applied %d, dead-lettered %+v", r.count(), w.written)
	}
}

// parkedFacts blocks every fact until ctx is done, then reports ctx.Err(): a unit of work interrupted by SIGTERM.
type parkedFacts struct {
	recorder
	entered chan struct{}
}

func (p *parkedFacts) park(ctx context.Context) error {
	close(p.entered)
	<-ctx.Done()
	return ctx.Err()
}

func (p *parkedFacts) OnAsrsCapacityChanged(ctx context.Context, _, _ string, _ facts.AsrsCapacity) error {
	return p.park(ctx)
}

func TestAShutdownWhileHandlingNeitherDeadLettersNorCommits(t *testing.T) {
	rec := &loopRecorder{}
	value := event("e-1", TypeAsrsCapacityChanged, map[string]any{"asrs_id": "ASRS1", "units_total": 1, "units_available": 1,
		"positions_total": 1, "positions_free": 1, "open_jobs": 0, "measured_at": "2026-11-20T08:00:01Z"})
	reader := &scriptedReader{rec: rec, msgs: []kafkago.Message{{Topic: AsrsTopic, Value: value}}}
	var logs bytes.Buffer
	w := &flakyDLQ{rec: rec}
	f := &parkedFacts{entered: make(chan struct{})}
	c := &Consumer{Name: "test", Topic: AsrsTopic, Reader: reader, Handlers: AsrsHandlers(f),
		Logger: slog.New(slog.NewTextHandler(&logs, nil)), DLQ: w}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	select {
	case <-f.entered:
	case <-time.After(5 * time.Second):
		t.Fatalf("the handler was never entered; events %v", rec.log())
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("run = %v, want context.Canceled", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("run did not return after cancel")
	}
	if got := rec.log(); !reflect.DeepEqual(got, []string{"fetch:0"}) || strings.Contains(logs.String(), "dead-letter") {
		t.Fatalf("events %v (want only the fetch); log:\n%s", got, logs.String())
	}
}

func TestTheDeadLetterWriterBatchesForTenMillisecondsOnTheMessageKey(t *testing.T) {
	w, ok := newDomainDLQWriter([]string{"127.0.0.1:1"}).(*kafkago.Writer)
	if !ok {
		t.Fatal("the dead-letter writer must be a *kafkago.Writer")
	}
	defer func() { _ = w.Close() }()
	if _, hash := w.Balancer.(*kafkago.Hash); !hash || w.BatchTimeout != 10*time.Millisecond {
		t.Fatalf("balancer %T, batch timeout %s", w.Balancer, w.BatchTimeout)
	}
}
