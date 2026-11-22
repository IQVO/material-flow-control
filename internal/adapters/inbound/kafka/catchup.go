package kafka

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	kafkago "github.com/segmentio/kafka-go"
)

// Subscription is one consumed topic and the consumer group that reads it.
type Subscription struct{ Topic, Group string }

// partitionEnd is one partition's retained range [first, last): last is the offset its NEXT record gets.
type partitionEnd struct{ first, last int64 }

// offsetSource is what CatchUp reads from the broker.
type offsetSource interface {
	// endOffsets is every partition's retained range; an unknown topic has no partitions.
	endOffsets(ctx context.Context, topic string) (map[int]partitionEnd, error)
	// committedOffsets is group's committed offset per partition (-1 when it never committed).
	committedOffsets(ctx context.Context, group, topic string, partitions []int) (map[int]int64, error)
}

// errNotSampled is the gate's answer until the end offsets of every topic were read once.
var errNotSampled = errors.New("the subsystem topics' end offsets are not sampled yet")

// CatchUp is the boot gate of ruling P4-R20: material-flow-control decides on local state learned from the two
// subsystem topics (decision D5), so it decides nothing (the POST routes, the leg-timeout tick) and does not report
// ready before both consumer groups have committed, on every partition, the end offset sampled once at boot. Facts
// published after boot never keep it closed; once open it stays open.
type CatchUp struct {
	subs []Subscription
	src  offsetSource
	poll time.Duration
	log  *slog.Logger

	mu     sync.Mutex
	ends   map[string]map[int]partitionEnd // sampled at boot, per topic
	status error                           // nil once caught up
}

// NewCatchUp builds the gate for subs over brokers; constructing it does not dial (Run does).
func NewCatchUp(brokers []string, logger *slog.Logger, subs ...Subscription) *CatchUp {
	return &CatchUp{subs: subs, src: brokerOffsets{client: &kafkago.Client{Addr: kafkago.TCP(brokers...), Timeout: 5 * time.Second}},
		poll: 250 * time.Millisecond, log: defaultLogger(logger)}
}

// Err is nil once both groups caught up with the backlog sampled at boot; until then it says what is behind.
func (g *CatchUp) Err() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.ends == nil && g.status == nil {
		return errNotSampled
	}
	return g.status
}

// Run samples the end offsets, then polls the committed offsets until the gate is open or ctx ends.
func (g *CatchUp) Run(ctx context.Context) {
	t := time.NewTicker(g.poll)
	defer t.Stop()
	for !g.step(ctx) {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// step is one check; it reports whether the gate is open.
func (g *CatchUp) step(ctx context.Context) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.ends != nil && g.status == nil {
		return true
	}
	if g.ends == nil {
		ends, err := g.sample(ctx)
		if err != nil {
			g.status = fmt.Errorf("%w: %w", errNotSampled, err)
			return false
		}
		g.ends = ends
	}
	g.status = g.behind(ctx)
	if g.status == nil {
		g.log.InfoContext(ctx, "caught up with the subsystem facts published before boot (ruling P4-R20)")
	}
	return g.status == nil
}

func (g *CatchUp) sample(ctx context.Context) (map[string]map[int]partitionEnd, error) {
	ends := make(map[string]map[int]partitionEnd, len(g.subs))
	for _, s := range g.subs {
		e, err := g.src.endOffsets(ctx, s.Topic)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", s.Topic, err)
		}
		ends[s.Topic] = e
	}
	return ends, nil
}

// behind names every topic whose group has not yet committed the sampled end on each of its partitions.
func (g *CatchUp) behind(ctx context.Context) error {
	var lagging []string
	for _, s := range g.subs {
		lag, err := g.lag(ctx, s)
		if err != nil {
			return fmt.Errorf("committed offsets of %s on %s: %w", s.Group, s.Topic, err)
		}
		if lag > 0 {
			lagging = append(lagging, fmt.Sprintf("%s (%d facts behind)", s.Topic, lag))
		}
	}
	if len(lagging) == 0 {
		return nil
	}
	return fmt.Errorf("catching up with the subsystem facts published before boot: %s", strings.Join(lagging, ", "))
}

// lag is how many records of the sampled ranges s.Group has still to commit; an empty partition has none.
func (g *CatchUp) lag(ctx context.Context, s Subscription) (int64, error) {
	var parts []int
	for p, e := range g.ends[s.Topic] {
		if e.last > e.first {
			parts = append(parts, p)
		}
	}
	if len(parts) == 0 {
		return 0, nil
	}
	slices.Sort(parts)
	committed, err := g.src.committedOffsets(ctx, s.Group, s.Topic, parts)
	if err != nil {
		return 0, err
	}
	var lag int64
	for _, p := range parts {
		e := g.ends[s.Topic][p]
		lag += max(e.last-max(committed[p], e.first), 0)
	}
	return lag, nil
}
