package kafka

import (
	"testing"

	kafkago "github.com/segmentio/kafka-go"
)

func TestTheReaderIsAFixedGroupAtLeastOnceReaderFromTheFirstOffset(t *testing.T) {
	cfg := readerConfig([]string{"127.0.0.1:9092"}, AsrsTopic, "material-flow-control.asrs-control-events")
	if cfg.GroupID != "material-flow-control.asrs-control-events" || cfg.Topic != "warehouse.asrs-control.events" {
		t.Fatalf("cfg = %+v", cfg)
	}
	if cfg.CommitInterval != 0 || cfg.MaxWait != 0 || cfg.StartOffset != kafkago.FirstOffset {
		t.Fatalf("CommitInterval %s (want synchronous), MaxWait %s (want unset), StartOffset %d", cfg.CommitInterval, cfg.MaxWait, cfg.StartOffset)
	}
}
