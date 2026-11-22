//go:build integration

package kafkatest

import (
	"strconv"
	"testing"
	"time"
)

// ruling P4-R19: a positional read (EndOffset as the mark, ReadFrom the mark) sees only what was written after it.
func TestEndOffsetIsTheMarkAndReadFromSeesOnlyWhatFollowsIt(t *testing.T) {
	topic := "kafkatest.mark." + strconv.FormatInt(time.Now().UnixNano(), 36)
	CreateTopic(t, topic)
	if got := EndOffset(t, topic); got != 0 {
		t.Fatalf("EndOffset of an empty topic = %d, want 0", got)
	}
	Produce(t, topic, "old-1", "old-2")
	mark := EndOffset(t, topic)
	if mark != 2 {
		t.Fatalf("EndOffset after two writes = %d, want 2", mark)
	}
	Produce(t, topic, "new-1")
	got := ReadFrom(t, topic, mark, func(ms []Message) bool { return len(ms) == 1 }, 30*time.Second)
	if got[0].Key != "new-1" || got[0].Offset != mark {
		t.Fatalf("ReadFrom(%d) = %+v, want only new-1 at offset %d", mark, got, mark)
	}
	if all := ReadAll(t, topic, func(ms []Message) bool { return len(ms) == 3 }, 30*time.Second); all[0].Key != "old-1" || all[2].Offset != 2 {
		t.Fatalf("ReadAll = %+v, want old-1..new-1 at offsets 0..2", all)
	}
}

// ruling P4-R19: a group that never committed has no committed offset (-1); the e2e floor waits until material-flow-
// control's groups have committed up to the subsystem topics' end offsets.
func TestCommittedIsMinusOneForAGroupThatNeverCommitted(t *testing.T) {
	topic := "kafkatest.committed." + strconv.FormatInt(time.Now().UnixNano(), 36)
	CreateTopic(t, topic)
	Produce(t, topic, "a")
	if got := Committed(t, "kafkatest-never-"+topic, topic); got != -1 {
		t.Fatalf("Committed of a group that never committed = %d, want -1", got)
	}
}
