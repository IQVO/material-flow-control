package kafka

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeOffsets is the broker as the gate sees it: per topic the end offsets and per group the committed ones.
type fakeOffsets struct {
	mu        sync.Mutex
	ends      map[string]map[int]partitionEnd
	committed map[string]map[int]int64 // group -> partition -> committed offset
	endErrs   int                      // the next endErrs ends() calls fail
	fetchErr  error
	endCalls  map[string]int
}

func (f *fakeOffsets) endOffsets(_ context.Context, topic string) (map[int]partitionEnd, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.endCalls == nil {
		f.endCalls = map[string]int{}
	}
	f.endCalls[topic]++
	if f.endErrs > 0 {
		f.endErrs--
		return nil, errors.New("broker down")
	}
	out := map[int]partitionEnd{}
	for p, e := range f.ends[topic] {
		out[p] = e
	}
	return out, nil
}

func (f *fakeOffsets) committedOffsets(_ context.Context, group, _ string, partitions []int) (map[int]int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fetchErr != nil {
		return nil, f.fetchErr
	}
	out := map[int]int64{}
	for _, p := range partitions {
		c, ok := f.committed[group][p]
		if !ok {
			c = -1
		}
		out[p] = c
	}
	return out, nil
}

func (f *fakeOffsets) commit(group string, partition int, offset int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.committed[group] == nil {
		f.committed[group] = map[int]int64{}
	}
	f.committed[group][partition] = offset
}

var gateSubs = []Subscription{{Topic: AsrsTopic, Group: "g-asrs"}, {Topic: AmrTopic, Group: "g-amr"}}

func newGate(f *fakeOffsets) *CatchUp {
	return &CatchUp{subs: gateSubs, src: f, poll: time.Millisecond, log: slog.New(slog.DiscardHandler)}
}

// ruling P4-R20: the gate stays closed until every partition of both topics committed the end offset sampled at
// boot, and its error names exactly the topics still behind.
func TestTheGateOpensOnlyWhenEveryPartitionCommittedTheEndSampledAtBoot(t *testing.T) {
	f := &fakeOffsets{ends: map[string]map[int]partitionEnd{AsrsTopic: {0: {0, 5}, 1: {0, 2}}, AmrTopic: {0: {0, 3}}},
		committed: map[string]map[int]int64{"g-amr": {0: 3}}}
	g := newGate(f)
	if err := g.Err(); err == nil {
		t.Fatal("Err before the first step = nil, want not caught up")
	}
	ctx := context.Background()
	for _, c := range []struct {
		partition int
		offset    int64
		open      bool
	}{{0, 4, false}, {1, 2, false}, {0, 5, true}} {
		f.commit("g-asrs", c.partition, c.offset)
		if got := g.step(ctx); got != c.open {
			t.Fatalf("after committing %d on partition %d: step = %v, want %v", c.offset, c.partition, got, c.open)
		}
		err := g.Err()
		if c.open != (err == nil) {
			t.Fatalf("after committing %d on partition %d: Err = %v, want open=%v", c.offset, c.partition, err, c.open)
		}
		if err != nil && (!strings.Contains(err.Error(), AsrsTopic) || strings.Contains(err.Error(), AmrTopic)) {
			t.Fatalf("Err = %q, want it to name %s only", err, AsrsTopic)
		}
	}
}

// The end offsets are sampled ONCE: facts published after boot never keep the gate closed.
func TestTheEndOffsetsAreSampledOnceAtBoot(t *testing.T) {
	f := &fakeOffsets{ends: map[string]map[int]partitionEnd{AsrsTopic: {0: {0, 5}}, AmrTopic: {0: {0, 1}}},
		committed: map[string]map[int]int64{"g-asrs": {0: 5}}}
	g := newGate(f)
	if g.step(context.Background()) {
		t.Fatal("open before the amr group committed its end offset")
	}
	f.mu.Lock()
	f.ends[AsrsTopic][0] = partitionEnd{0, 500}
	f.mu.Unlock()
	f.commit("g-amr", 0, 1)
	if !g.step(context.Background()) || g.Err() != nil {
		t.Fatalf("Err = %v, want open: the asrs end grew after boot", g.Err())
	}
	if f.endCalls[AsrsTopic] != 1 || f.endCalls[AmrTopic] != 1 {
		t.Fatalf("end offsets sampled %v times, want once per topic", f.endCalls)
	}
}

// An empty partition (nothing published, or retention removed everything) and an unknown topic have nothing to replay.
func TestAnEmptyPartitionOrAnUnknownTopicHasNothingToReplay(t *testing.T) {
	f := &fakeOffsets{ends: map[string]map[int]partitionEnd{AsrsTopic: {0: {0, 0}, 1: {7, 7}}}, committed: map[string]map[int]int64{}}
	g := newGate(f)
	if !g.step(context.Background()) || g.Err() != nil {
		t.Fatalf("Err = %v, want open: no partition holds a fact and the amr topic does not exist", g.Err())
	}
}

// A committed offset below the first retained one still has to reach the end sampled at boot.
func TestACommittedOffsetBelowTheRetainedStartIsStillBehind(t *testing.T) {
	f := &fakeOffsets{ends: map[string]map[int]partitionEnd{AsrsTopic: {0: {7, 9}}}, committed: map[string]map[int]int64{"g-asrs": {0: 3}}}
	g := newGate(f)
	if g.step(context.Background()) {
		t.Fatal("open while the asrs group committed 3 of [7, 9)")
	}
	f.commit("g-asrs", 0, 8)
	if g.step(context.Background()) {
		t.Fatal("open while the asrs group committed 8 of [7, 9)")
	}
	f.commit("g-asrs", 0, 9)
	if !g.step(context.Background()) {
		t.Fatal("still closed after the asrs group committed 9 of [7, 9)")
	}
}

// A partition committed past its sampled end (facts after boot) does not make up for another one still behind.
func TestAPartitionPastItsEndDoesNotHideAnotherOneBehind(t *testing.T) {
	f := &fakeOffsets{ends: map[string]map[int]partitionEnd{AsrsTopic: {0: {0, 5}, 1: {0, 5}}},
		committed: map[string]map[int]int64{"g-asrs": {0: 50, 1: 0}}}
	g := newGate(f)
	if g.step(context.Background()) {
		t.Fatal("open while partition 1 committed 0 of 5")
	}
	if !strings.Contains(g.Err().Error(), "(5 facts behind)") {
		t.Fatalf("Err = %q, want 5 facts behind", g.Err())
	}
}

// The broker not answering keeps the gate closed (never open by default) and is retried on the next step.
func TestTheGateStaysClosedWhileTheBrokerDoesNotAnswer(t *testing.T) {
	f := &fakeOffsets{ends: map[string]map[int]partitionEnd{AsrsTopic: {0: {0, 1}}}, committed: map[string]map[int]int64{"g-asrs": {0: 1}},
		endErrs: 1}
	g := newGate(f)
	if g.step(context.Background()) || !strings.Contains(g.Err().Error(), "not sampled") {
		t.Fatalf("Err = %v, want closed: the end offsets are not sampled yet", g.Err())
	}
	f.fetchErr = errors.New("coordinator loading")
	if g.step(context.Background()) || g.Err() == nil {
		t.Fatal("open while the committed offsets cannot be fetched")
	}
	f.fetchErr = nil
	if !g.step(context.Background()) || g.Err() != nil {
		t.Fatalf("Err = %v, want open once the broker answers", g.Err())
	}
	if f.endCalls[AsrsTopic] != 2 {
		t.Fatalf("asrs end offsets sampled %d times, want 2 (one failure, one success)", f.endCalls[AsrsTopic])
	}
}

// Once open the gate stays open and Run returns; Run also returns when ctx ends while still behind.
func TestRunReturnsOnceOpenAndTheGateStaysOpen(t *testing.T) {
	f := &fakeOffsets{ends: map[string]map[int]partitionEnd{AsrsTopic: {0: {0, 1}}}, committed: map[string]map[int]int64{"g-asrs": {0: 1}}}
	g := newGate(f)
	g.Run(context.Background())
	if g.Err() != nil {
		t.Fatalf("Err after Run returned = %v, want open", g.Err())
	}
	f.fetchErr = errors.New("broker gone")
	if !g.step(context.Background()) || g.Err() != nil {
		t.Fatal("an open gate closed again")
	}

	behind := newGate(&fakeOffsets{ends: map[string]map[int]partitionEnd{AsrsTopic: {0: {0, 1}}}, committed: map[string]map[int]int64{}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	behind.Run(ctx)
	if behind.Err() == nil {
		t.Fatal("a cancelled Run opened the gate while behind")
	}
}
