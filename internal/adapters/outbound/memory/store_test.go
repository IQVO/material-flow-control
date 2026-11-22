package memory_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/IQVO/material-flow-control/internal/adapters/outbound/memory"
	"github.com/IQVO/material-flow-control/internal/application/command"
	"github.com/IQVO/material-flow-control/internal/application/ports"
	ct "github.com/IQVO/material-flow-control/internal/domain/containertrack"
)

var t0 = time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)

func TestAFailedUnitOfWorkRollsEveryTableBackAndDropsItsHooks(t *testing.T) {
	s := memory.NewStore()
	uow, tracks, cmds := memory.NewUnitOfWork(s), memory.NewTracks(s), memory.NewCommands(s)
	ran := 0
	boom := errors.New("boom")
	err := uow.Do(context.Background(), func(ctx context.Context) error {
		tr := ct.New("TOTE-1", t0)
		tr.MoveTo(ct.Position{Kind: ct.AtStation, Code: "ST1"}, t0)
		if err := tracks.Save(ctx, tr); err != nil {
			return err
		}
		if err := cmds.Enqueue(ctx, command.New("c1", "to-1", "to-1.1.1", command.RequestLeg, t0, time.Minute)); err != nil {
			return err
		}
		uow.AfterCommit(ctx, func() { ran++ })
		return boom
	})
	if !errors.Is(err, boom) || ran != 0 {
		t.Fatalf("Do = %v, hooks ran %d", err, ran)
	}
	if _, err := tracks.Get(context.Background(), "TOTE-1"); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("rolled-back track = %v", err)
	}
	if len(cmds.All()) != 0 {
		t.Fatal("rolled-back command")
	}
	_ = uow.Do(context.Background(), func(ctx context.Context) error {
		uow.AfterCommit(ctx, func() { ran++ })
		return uow.Do(ctx, func(inner context.Context) error {
			uow.AfterCommit(inner, func() { ran += 10 })
			return nil
		})
	})
	if ran != 11 {
		t.Fatalf("hooks of the outermost unit of work = %d, want 11", ran)
	}
}

func TestCommandsAreDueInCreationOrderAndHistoryIsNewestFirst(t *testing.T) {
	s := memory.NewStore()
	ctx := context.Background()
	cmds, tracks := memory.NewCommands(s), memory.NewTracks(s)
	_ = cmds.Enqueue(ctx, command.New("a", "to-1", "r1", command.RequestLeg, t0, time.Minute))
	later := command.New("b", "to-2", "r2", command.RequestLeg, t0, time.Minute)
	_ = cmds.Enqueue(ctx, later)
	_ = cmds.Enqueue(ctx, command.New("c", "to-3", "r3", command.RequestLeg, t0, time.Minute))
	all := cmds.All()
	retried := all[1].Retry(t0, time.Second, time.Minute, "x")
	_ = cmds.Save(ctx, retried)
	due, _ := cmds.Due(ctx, t0, 10)
	if len(due) != 2 || due[0].ID != "a" || due[1].ID != "c" || due[0].Seq != 1 || due[1].Seq != 3 {
		t.Fatalf("due = %+v", due)
	}
	if due, _ := cmds.Due(ctx, t0.Add(time.Second), 1); len(due) != 1 || due[0].ID != "a" {
		t.Fatalf("limit = %+v", due)
	}
	tr := ct.New("TOTE-1", t0)
	tr.MoveTo(ct.Position{Kind: ct.AtStation, Code: "ST1"}, t0)
	tr.MoveTo(ct.Position{Kind: ct.AtHandoff, Code: "PK"}, t0.Add(time.Second))
	_ = tracks.Save(ctx, tr)
	h, _ := tracks.History(ctx, "TOTE-1", 1)
	if len(h) != 1 || h[0].To.Code != "PK" {
		t.Fatalf("history = %+v", h)
	}
	got, _ := tracks.Get(ctx, "TOTE-1")
	if got.Version() != 1 || got.Position().Code != "PK" {
		t.Fatalf("saved track = %+v", got.Snapshot())
	}
}

func TestTheInboxClaimsAnIDOnce(t *testing.T) {
	s := memory.NewStore()
	in := memory.NewInbox(s)
	first, _ := in.Claim(context.Background(), "e1", "T", t0)
	again, _ := in.Claim(context.Background(), "e1", "T", t0)
	if !first || again {
		t.Fatalf("claims = %v %v", first, again)
	}
}
