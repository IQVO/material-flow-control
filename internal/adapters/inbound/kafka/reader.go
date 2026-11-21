package kafka

import kafkago "github.com/segmentio/kafka-go"

// readerConfig is the reader of both fixed-group consumers. GroupID always comes from the caller (an environment
// variable at the composition root, never a literal). CommitInterval is left UNSET, so CommitMessages commits
// synchronously after a message was handled; MaxWait is left unset (checklist item 12: kafka-go uses it as the
// read deadline of every fetch). StartOffset FirstOffset: a new group learns every ContainerStored ever published
// within the topic's retention (decision D6).
func readerConfig(brokers []string, topic, groupID string) kafkago.ReaderConfig {
	return kafkago.ReaderConfig{
		Brokers:     brokers,
		Topic:       topic,
		GroupID:     groupID,
		StartOffset: kafkago.FirstOffset,
	}
}
