package kafka

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"
)

// blockingOffsets is a broker whose endOffsets (block "end") or committedOffsets (block "committed") call hangs until
// release is closed; entered is closed when the first hanging call starts.
type blockingOffsets struct {
	*fakeOffsets
	block            string
	entered, release chan struct{}
	once             sync.Once
}

func (b *blockingOffsets) wait(call string) {
	if b.block == call {
		b.once.Do(func() { close(b.entered) })
		<-b.release
	}
}

func (b *blockingOffsets) endOffsets(ctx context.Context, topic string) (map[int]partitionEnd, error) {
	b.wait("end")
	return b.fakeOffsets.endOffsets(ctx, topic)
}

func (b *blockingOffsets) committedOffsets(ctx context.Context, group, topic string, partitions []int) (map[int]int64, error) {
	b.wait("committed")
	return b.fakeOffsets.committedOffsets(ctx, group, topic, partitions)
}

// carry (Task 26 review): step does its broker I/O outside the gate's lock, so Err (every POST, /readyz, the tick)
// answers at once while a sampling or an offset-fetch call is hung on a slow broker; the gate stays closed meanwhile
// and opens once the call returns.
func TestErrAnswersAtOnceWhileABrokerCallIsBlocked(t *testing.T) {
	for _, block := range []string{"end", "committed"} {
		t.Run(block, func(t *testing.T) {
			b := &blockingOffsets{fakeOffsets: &fakeOffsets{ends: map[string]map[int]partitionEnd{AsrsTopic: {0: {0, 1}}},
				committed: map[string]map[int]int64{"g-asrs": {0: 1}}}, block: block,
				entered: make(chan struct{}), release: make(chan struct{})}
			g := &CatchUp{subs: gateSubs, src: b, poll: time.Millisecond, log: slog.New(slog.DiscardHandler)}
			opened := make(chan bool, 1)
			go func() { opened <- g.step(context.Background()) }()
			<-b.entered
			answered := make(chan error, 1)
			go func() { answered <- g.Err() }()
			select {
			case err := <-answered:
				if err == nil {
					t.Error("Err = nil while the broker call is still blocked, want closed")
				}
			case <-time.After(time.Second):
				t.Errorf("Err blocked for 1 s behind a hung %s call: the lock is held across broker I/O", block)
			}
			close(b.release)
			if !<-opened || g.Err() != nil {
				t.Fatalf("after the %s call returned: Err = %v, want open", block, g.Err())
			}
		})
	}
}
