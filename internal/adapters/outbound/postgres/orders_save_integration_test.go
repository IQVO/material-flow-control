//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"

	"github.com/IQVO/material-flow-control/internal/adapters/outbound/postgres"
	"github.com/IQVO/material-flow-control/internal/application/ports"
	"github.com/IQVO/material-flow-control/internal/domain/layout"
	"github.com/IQVO/material-flow-control/internal/domain/routing"
	to "github.com/IQVO/material-flow-control/internal/domain/transportorder"
	"github.com/IQVO/material-flow-control/internal/testing/pgtest"
)

// carry (Task 13 review): saving an order that was never inserted updates no row; it is ports.ErrNotFound, and no
// leg reference is written for it (the unit of work rolls back).
func TestSavingAnOrderThatWasNeverInsertedIsNotFound(t *testing.T) {
	pool := pgtest.Pool(t)
	ctx := context.Background()
	src := layout.Place{Kind: layout.PlaceHandoff, Code: "WH1-RCV-DEC1", HandoffPointID: "ST2", HandoffKind: layout.HandoffDecant}
	dst := layout.Place{Kind: layout.PlaceHandoff, Code: "WH1-PCK-PK1", HandoffPointID: "PK1", HandoffKind: layout.HandoffPack}
	o, _ := to.Accept("to-ghost", request(), src, dst, []routing.LegPlan{{Subsystem: routing.SubsystemAmr, SubsystemID: "AMR1",
		Kind: routing.KindMove, From: src, To: dst}}, t0)
	if err := postgres.NewOrders(pool).Save(ctx, o); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("Save(never inserted) = %v, want ports.ErrNotFound", err)
	}
	var legs int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM transport_legs WHERE transport_order_id = 'to-ghost'`).Scan(&legs); err != nil || legs != 0 {
		t.Fatalf("leg references of the unsaved order = %d (%v), want none", legs, err)
	}
}
