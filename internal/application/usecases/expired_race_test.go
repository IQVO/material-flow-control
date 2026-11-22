package usecases_test

import (
	"context"
	"errors"
	"testing"

	"github.com/IQVO/material-flow-control/internal/adapters/outbound/memory"
	"github.com/IQVO/material-flow-control/internal/application/usecases"
	"github.com/IQVO/material-flow-control/internal/config"
	tr "github.com/IQVO/material-flow-control/internal/domain/transportrequest"
)

// purgingRequests returns the record, then lets the order purge commit before the caller reads the order: a GET
// runs outside a unit of work, so nothing holds the two reads to one snapshot.
type purgingRequests struct {
	*memory.Requests
	purge func()
}

func (r purgingRequests) Get(ctx context.Context, id string) (tr.Record, error) {
	rec, err := r.Requests.Get(ctx, id)
	r.purge()
	return rec, err
}

// carry (retention review): the GET race. The record is read, the retention purges the order, then the order read
// finds nothing: the read answers ErrTransportExpired, never the repository's not-found (a 500).
func TestARecordReadRacingTheOrderPurgeAnswersExpired(t *testing.T) {
	e := newEnv(t)
	e.stored("TOTE-1")
	v := e.mustAccept("rq-1", "TOTE-1", asrsLoc, "ST1", tr.PurposePick)
	l, err := config.LoadLayout("../../../config/flow-layout.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	r, err := usecases.NewReader(l, usecases.DefaultConfig(), usecases.ReadDeps{
		Requests: purgingRequests{memory.NewRequests(e.store), func() { e.store.PurgeOrder(v.OrderID) }},
		Orders:   e.orders, Tracks: e.tracks, Stations: memory.NewStations(e.store), Capacities: e.caps,
		Units: memory.NewUnits(e.store), Clock: e.clock})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Transport(ctx, "rq-1"); !errors.Is(err, usecases.ErrTransportExpired) {
		t.Fatalf("a read racing the order purge = %v, want ErrTransportExpired", err)
	}
}
