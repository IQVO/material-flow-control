// Package routing is the pure route policy (ADR 0002): the legs that move a container from a source place to a
// destination place through handoff points. The WES chooses the container and the destination; this package
// chooses the route (seam decision 3).
package routing

import (
	"errors"

	"github.com/IQVO/material-flow-control/internal/domain/layout"
)

var (
	// ErrUnreachable: no route exists (AS/RS to AS/RS, a port as destination, a place no robot reaches).
	ErrUnreachable = errors.New("destination unreachable")
	// ErrSamePlace: source and destination are one place.
	ErrSamePlace = errors.New("source and destination are the same place")
)

// Subsystem names the controller that executes a leg.
type Subsystem string

const (
	SubsystemAsrs Subsystem = "ASRS"
	SubsystemAmr  Subsystem = "AMR_FLEET"
)

// LegKind is what a leg asks its subsystem to do.
type LegKind string

const (
	KindRetrieve LegKind = "RETRIEVE"
	KindStore    LegKind = "STORE"
	KindMove     LegKind = "MOVE"
)

// LegPlan is one planned leg. A zero From is the output port the previous retrieve is presented at; a zero To
// of a MOVE is the input port chosen when the leg is dispatched, and a zero From of a STORE is the port the
// previous MOVE dropped at (ADR 0002).
type LegPlan struct {
	Subsystem   Subsystem
	SubsystemID string
	Kind        LegKind
	From        layout.Place
	To          layout.Place
}

// Plan returns the legs from src to dst.
func Plan(l *layout.Layout, src, dst layout.Place) ([]LegPlan, error) {
	if src.Code == dst.Code {
		return nil, ErrSamePlace
	}
	if dst.Kind == layout.PlacePort {
		return nil, ErrUnreachable
	}
	fleet := l.Fleet().FleetID
	if src.Kind == layout.PlaceAsrs {
		if dst.Kind == layout.PlaceAsrs || !dst.AMR() {
			return nil, ErrUnreachable
		}
		return []LegPlan{
			{Subsystem: SubsystemAsrs, SubsystemID: src.AsrsID, Kind: KindRetrieve, From: src},
			{Subsystem: SubsystemAmr, SubsystemID: fleet, Kind: KindMove, To: dst},
		}, nil
	}
	if !src.AMR() {
		return nil, ErrUnreachable
	}
	if dst.Kind == layout.PlaceAsrs {
		return []LegPlan{
			{Subsystem: SubsystemAmr, SubsystemID: fleet, Kind: KindMove, From: src},
			{Subsystem: SubsystemAsrs, SubsystemID: dst.AsrsID, Kind: KindStore, To: dst},
		}, nil
	}
	if !dst.AMR() {
		return nil, ErrUnreachable
	}
	return []LegPlan{{Subsystem: SubsystemAmr, SubsystemID: fleet, Kind: KindMove, From: src, To: dst}}, nil
}

// StoreViaOtherInput re-plans a store whose input port failed (decision D13): a MOVE from the failed input port
// to the next input port of the same AS/RS in layout order, then a STORE there.
func StoreViaOtherInput(l *layout.Layout, failed, asrs layout.Place) ([]LegPlan, error) {
	for _, in := range l.InputPorts(asrs.AsrsID) {
		if in.PortID == failed.PortID {
			continue
		}
		return []LegPlan{
			{Subsystem: SubsystemAmr, SubsystemID: l.Fleet().FleetID, Kind: KindMove, From: failed, To: in},
			{Subsystem: SubsystemAsrs, SubsystemID: asrs.AsrsID, Kind: KindStore, From: in, To: asrs},
		}, nil
	}
	return nil, ErrUnreachable
}
