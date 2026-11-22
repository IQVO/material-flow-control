package kafka_test

import (
	"context"
	"testing"

	kafkago "github.com/segmentio/kafka-go"

	"github.com/IQVO/material-flow-control/internal/adapters/outbound/kafka"
	"github.com/IQVO/material-flow-control/internal/application/outbox"
)

type fakeWriter struct{ got []kafkago.Message }

func (f *fakeWriter) WriteMessages(_ context.Context, msgs ...kafkago.Message) error {
	f.got = append(f.got, msgs...)
	return nil
}

func TestTheSinkRoutesEachMessageToItsOwnTopicWithItsHeaders(t *testing.T) {
	w := &fakeWriter{}
	s := kafka.NewRelaySinkWithWriter(w)
	err := s.Send(context.Background(),
		outbox.Message{Topic: "warehouse.material-flow-control.events", Key: []byte("to-1"), Value: []byte("{}"), Headers: []outbox.Header{{Key: "content-type", Value: "x"}}},
		outbox.Message{Topic: "warehouse.material-flow-control.analytics", Key: []byte("to-1"), Value: []byte("{}")})
	if err != nil || len(w.got) != 2 || w.got[0].Topic != "warehouse.material-flow-control.events" || w.got[1].Topic != "warehouse.material-flow-control.analytics" ||
		string(w.got[0].Key) != "to-1" || w.got[0].Headers[0].Key != "content-type" {
		t.Fatalf("Send = %v, %+v", err, w.got)
	}
	if err := s.Send(context.Background(), outbox.Message{EventType: "t"}); err == nil {
		t.Fatal("a message without a topic was sent")
	}
}
