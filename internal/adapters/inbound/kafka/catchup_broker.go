package kafka

import (
	"context"
	"errors"
	"fmt"

	kafkago "github.com/segmentio/kafka-go"
)

// brokerOffsets reads the offsets CatchUp compares from the cluster (ruling P4-R20).
type brokerOffsets struct{ client *kafkago.Client }

// endOffsets lists every partition of topic with its first and next offsets; an unknown topic has none (nothing was
// ever published to it, so there is nothing to replay).
func (b brokerOffsets) endOffsets(ctx context.Context, topic string) (map[int]partitionEnd, error) {
	meta, err := b.client.Metadata(ctx, &kafkago.MetadataRequest{Topics: []string{topic}})
	if err != nil {
		return nil, err
	}
	var reqs []kafkago.OffsetRequest
	for _, t := range meta.Topics {
		if t.Name != topic {
			continue
		}
		if errors.Is(t.Error, kafkago.UnknownTopicOrPartition) {
			return map[int]partitionEnd{}, nil
		}
		if t.Error != nil {
			return nil, t.Error
		}
		for _, p := range t.Partitions {
			reqs = append(reqs, kafkago.FirstOffsetOf(p.ID), kafkago.LastOffsetOf(p.ID))
		}
	}
	out := map[int]partitionEnd{}
	if len(reqs) == 0 {
		return out, nil
	}
	res, err := b.client.ListOffsets(ctx, &kafkago.ListOffsetsRequest{Topics: map[string][]kafkago.OffsetRequest{topic: reqs}})
	if err != nil {
		return nil, err
	}
	for _, p := range res.Topics[topic] {
		if p.Error != nil {
			return nil, fmt.Errorf("partition %d: %w", p.Partition, p.Error)
		}
		out[p.Partition] = partitionEnd{first: p.FirstOffset, last: p.LastOffset}
	}
	return out, nil
}

// committedOffsets is group's committed offset on each partition (-1 when it never committed).
func (b brokerOffsets) committedOffsets(ctx context.Context, group, topic string, partitions []int) (map[int]int64, error) {
	res, err := b.client.OffsetFetch(ctx, &kafkago.OffsetFetchRequest{GroupID: group, Topics: map[string][]int{topic: partitions}})
	if err != nil {
		return nil, err
	}
	if res.Error != nil {
		return nil, res.Error
	}
	out := make(map[int]int64, len(partitions))
	for _, p := range res.Topics[topic] {
		if p.Error != nil {
			return nil, fmt.Errorf("partition %d: %w", p.Partition, p.Error)
		}
		out[p.Partition] = p.CommittedOffset
	}
	for _, p := range partitions {
		if _, ok := out[p]; !ok {
			return nil, fmt.Errorf("partition %d: no committed offset in the answer", p)
		}
	}
	return out, nil
}
