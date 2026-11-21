package main

import (
	"log/slog"
	"testing"

	kafkago "github.com/segmentio/kafka-go"
)

// carry (Task 19 brief / fleet rule): the consumer groups come from MFC_ASRS_CONSUMER_GROUP and
// MFC_AMR_CONSUMER_GROUP, never a literal at the call site, so a harness run joins its own groups and
// never the live Deployment's.
func TestTheConsumerGroupsComeFromTheEnvironment(t *testing.T) {
	m := full()
	m["MFC_ASRS_CONSUMER_GROUP"] = "e2e-1.asrs"
	m["MFC_AMR_CONSUMER_GROUP"] = "e2e-1.amr"
	c, err := loadConfig(env(m))
	if err != nil {
		t.Fatal(err)
	}
	cs := newConsumers(c, nil, slog.New(slog.DiscardHandler))
	t.Cleanup(func() {
		for _, x := range cs {
			_ = x.Close()
		}
	})
	want := []struct{ name, topic, group string }{
		{"asrs-control-events", "warehouse.asrs-control.events", "e2e-1.asrs"},
		{"amr-fleet-control-events", "warehouse.amr-fleet-control.events", "e2e-1.amr"},
	}
	if len(cs) != len(want) {
		t.Fatalf("consumers = %d", len(cs))
	}
	for i, w := range want {
		rc := cs[i].Reader.(*kafkago.Reader).Config()
		if cs[i].Name != w.name || rc.Topic != w.topic || rc.GroupID != w.group {
			t.Fatalf("consumer %d = %s %s group %s, want %+v", i, cs[i].Name, rc.Topic, rc.GroupID, w)
		}
	}
}
