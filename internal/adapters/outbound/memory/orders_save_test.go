package memory_test

import (
	"context"
	"errors"
	"testing"

	"github.com/IQVO/material-flow-control/internal/adapters/outbound/memory"
	"github.com/IQVO/material-flow-control/internal/application/ports"
	"github.com/IQVO/material-flow-control/internal/domain/layout"
	"github.com/IQVO/material-flow-control/internal/domain/routing"
	to "github.com/IQVO/material-flow-control/internal/domain/transportorder"
	tr "github.com/IQVO/material-flow-control/internal/domain/transportrequest"
)

// carry (Task 13 review): the double agrees with Postgres: saving an order that was never inserted is
// ports.ErrNotFound and stores nothing.
func TestSavingAnOrderThatWasNeverInsertedIsNotFound(t *testing.T) {
	s := memory.NewStore()
	orders := memory.NewOrders(s)
	src := layout.Place{Kind: layout.PlaceHandoff, Code: "WH1-RCV-DEC1", HandoffPointID: "ST2", HandoffKind: layout.HandoffDecant}
	dst := layout.Place{Kind: layout.PlaceHandoff, Code: "WH1-PCK-PK1", HandoffPointID: "PK1", HandoffKind: layout.HandoffPack}
	req := tr.Request{ID: "rq-g", ContainerID: "TOTE-G", Source: src.Code, Destination: dst.Code, Purpose: tr.PurposeOutbound,
		Priority: tr.PriorityNormal, DueBy: t0}
	o, _ := to.Accept("to-ghost", req, src, dst, []routing.LegPlan{{Subsystem: routing.SubsystemAmr, SubsystemID: "AMR1",
		Kind: routing.KindMove, From: src, To: dst}}, t0)
	if err := orders.Save(context.Background(), o); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("Save(never inserted) = %v, want ports.ErrNotFound", err)
	}
	if _, err := orders.Get(context.Background(), "to-ghost"); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("Get after the refused save = %v, want ports.ErrNotFound", err)
	}
}
