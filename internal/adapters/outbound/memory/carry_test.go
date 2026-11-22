package memory_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/IQVO/material-flow-control/internal/adapters/outbound/memory"
	"github.com/IQVO/material-flow-control/internal/application/command"
	"github.com/IQVO/material-flow-control/internal/application/facts"
	"github.com/IQVO/material-flow-control/internal/application/ports"
	ct "github.com/IQVO/material-flow-control/internal/domain/containertrack"
)

// carry (Task 8 review): an AfterCommit hook runs after the unit of work released the store, so a hook that opens a
// new unit of work returns instead of deadlocking on the transaction mutex.
func TestAHookThatOpensANewUnitOfWorkReturns(t *testing.T) {
	s := memory.NewStore()
	uow, tracks := memory.NewUnitOfWork(s), memory.NewTracks(s)
	done := make(chan error, 1)
	go func() {
		done <- uow.Do(context.Background(), func(ctx context.Context) error {
			uow.AfterCommit(ctx, func() {
				_ = uow.Do(context.Background(), func(ctx context.Context) error { return tracks.Save(ctx, ct.New("TOTE-H", t0)) })
			})
			return nil
		})
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Do = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a hook that opens a new unit of work deadlocked")
	}
	if _, err := tracks.Get(context.Background(), "TOTE-H"); err != nil {
		t.Fatalf("the hook's own unit of work did not commit: %v", err)
	}
}

// carry (Task 8 review): a panicking unit of work restores every table, drops its hooks and re-panics.
func TestAPanickingUnitOfWorkRollsBackDropsItsHooksAndRepanics(t *testing.T) {
	s := memory.NewStore()
	uow, tracks := memory.NewUnitOfWork(s), memory.NewTracks(s)
	ran := false
	func() {
		defer func() {
			if r := recover(); r != "boom" {
				t.Fatalf("recovered %v, want the unit of work to re-panic with boom", r)
			}
		}()
		_ = uow.Do(context.Background(), func(ctx context.Context) error {
			if err := tracks.Save(ctx, ct.New("TOTE-P", t0)); err != nil {
				return err
			}
			uow.AfterCommit(ctx, func() { ran = true })
			panic("boom")
		})
	}()
	if _, err := tracks.Get(context.Background(), "TOTE-P"); !errors.Is(err, ports.ErrNotFound) || ran {
		t.Fatalf("after the panic: track %v, hook ran %v; want rolled back, not run", err, ran)
	}
	// The store is usable again: the panic released the transaction mutex.
	if err := uow.Do(context.Background(), func(ctx context.Context) error { return tracks.Save(ctx, ct.New("TOTE-Q", t0)) }); err != nil {
		t.Fatal(err)
	}
}

// carry (Task 8 review): FailNext fails exactly the NEXT write of the named repository with ErrInjected; the
// repositories it does not name (units, inbox, idempotency) ignore it.
func TestFailNextFailsExactlyTheNextWriteOfThatRepository(t *testing.T) {
	ctx := context.Background()
	s := memory.NewStore()
	tracks, cmds, events, units := memory.NewTracks(s), memory.NewCommands(s), memory.NewEvents(s), memory.NewUnits(s)
	for _, c := range []struct {
		repo  string
		write func() error
	}{
		{"tracks", func() error { return tracks.Save(ctx, ct.New("TOTE-F", t0)) }},
		{"commands", func() error {
			return cmds.Enqueue(ctx, command.New("c-"+time.Now().String(), "to-1", "to-1.1.1", command.RequestLeg, t0, time.Minute))
		}},
		{"events", func() error { return events.Append(ctx, "ev") }},
	} {
		s.FailNext(c.repo)
		if err := c.write(); !errors.Is(err, memory.ErrInjected) {
			t.Fatalf("%s: armed write = %v, want ErrInjected", c.repo, err)
		}
		if err := c.write(); err != nil {
			t.Fatalf("%s: the write after the injected one = %v, want nil", c.repo, err)
		}
	}
	s.FailNext("units")
	if err := units.Save(ctx, facts.EquipmentState{AsrsID: "ASRS1", UnitID: "S1"}); err != nil {
		t.Fatalf("units ignores FailNext, got %v", err)
	}
}

// carry (Task 8 review): SetRecommended and RecommendedInputPort share the script's mutex (go test -race).
func TestSetRecommendedIsSafeWhileTheDispatcherReadsIt(t *testing.T) {
	a := memory.NewAsrs()
	var wg sync.WaitGroup
	wg.Go(func() {
		for range 100 {
			a.SetRecommended("IN2", nil)
		}
	})
	wg.Go(func() {
		for range 100 {
			_, _ = a.RecommendedInputPort(context.Background(), "ASRS1")
		}
	})
	wg.Wait()
	a.SetRecommended("", errors.New("down"))
	if p, err := a.RecommendedInputPort(context.Background(), "ASRS1"); p != "" || err == nil || err.Error() != "down" {
		t.Fatalf("RecommendedInputPort = %q, %v; want \"\", down", p, err)
	}
}
