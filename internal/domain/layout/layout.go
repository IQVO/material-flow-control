// Package layout is the material-flow layout (ADR 0002): the AS/RS systems with their ports, the AMR fleet, the
// goods-to-person stations with their buffer limits (ADR 0001), the other handoffs and the clarification points,
// and the places a transport request can name.
package layout

import (
	"errors"
	"fmt"
	"strings"
)

// ErrInvalid is wrapped by every layout validation error.
var ErrInvalid = errors.New("invalid material-flow layout")

// PortKind is an AS/RS port's direction.
type PortKind string

const (
	PortInput  PortKind = "INPUT"
	PortOutput PortKind = "OUTPUT"
)

// HandoffKind says what a non-station AMR place is for.
type HandoffKind string

const (
	HandoffDecant  HandoffKind = "DECANT"
	HandoffPack    HandoffKind = "PACK"
	HandoffDock    HandoffKind = "DOCK"
	HandoffForward HandoffKind = "FORWARD"
	HandoffOther   HandoffKind = "OTHER"
)

// Fleet is the one AMR fleet; LegsPerRobotHour is the rate capacity_per_hour is computed with.
type Fleet struct {
	FleetID          string
	LegsPerRobotHour int
}

// Port is one AS/RS port: its asrs-control id, direction, location code and AMR handoff point.
type Port struct {
	PortID         string
	Kind           PortKind
	LocationCode   string
	HandoffPointID string
}

// Asrs is one AS/RS: its asrs-control id and its one logical location.
type Asrs struct {
	AsrsID                string
	LocationCode          string
	RetrievalsPerUnitHour int
	Ports                 []Port
}

// Station is one goods-to-person station and its buffer limit.
type Station struct {
	StationID      string
	LocationCode   string
	HandoffPointID string
	BufferLimit    int
}

// Handoff is any other AMR place a request can name.
type Handoff struct {
	LocationCode   string
	HandoffPointID string
	Kind           HandoffKind
}

// ClarificationPoint is where unknown or mismatched containers go.
type ClarificationPoint struct {
	LocationCode   string
	HandoffPointID string
}

// Spec is the layout as configured.
type Spec struct {
	SiteID              string
	Fleet               Fleet
	Asrs                []Asrs
	Stations            []Station
	Handoffs            []Handoff
	ClarificationPoints []ClarificationPoint
}

// Layout is a validated Spec with its lookups. It is immutable after New.
type Layout struct {
	spec      Spec
	byRef     map[string]Place
	byHandoff map[string]Place
	ports     map[string]Place
	asrs      map[string]Asrs
	stations  map[string]Station
	inputs    map[string][]Place
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

// carry (Task 3 review): Resolve, ByHandoffPoint and Port compare exactly, so an id or location code with
// surrounding whitespace would never match a request or a fact. checkTrimmed refuses one, naming the entry and
// field; fields holds name, value pairs. A value that is only whitespace is left to the "required" checks.
func checkTrimmed(entry string, fields ...string) error {
	for i := 1; i < len(fields); i += 2 { // fields comes in name, value pairs: fields[i] is the value of fields[i-1]
		if v := fields[i]; !blank(v) && v != strings.TrimSpace(v) {
			return invalid("%s: %s %q has surrounding whitespace", entry, fields[i-1], v)
		}
	}
	return nil
}

// New validates s and builds its lookups.
func New(s Spec) (*Layout, error) {
	if err := checkHeader(s); err != nil {
		return nil, err
	}
	s = cloneSpec(s)
	l := &Layout{spec: s, byRef: map[string]Place{}, byHandoff: map[string]Place{}, ports: map[string]Place{},
		asrs: map[string]Asrs{}, stations: map[string]Station{}, inputs: map[string][]Place{}}
	for i, a := range s.Asrs {
		if err := l.addAsrs(i, a); err != nil {
			return nil, err
		}
	}
	for i, st := range s.Stations {
		if err := l.addStation(i, st); err != nil {
			return nil, err
		}
	}
	for i, h := range s.Handoffs {
		if err := checkTrimmed(fmt.Sprintf("handoffs[%d]", i), "locationCode", h.LocationCode, "handoffPointId", h.HandoffPointID); err != nil {
			return nil, err
		}
		if err := l.addHandoff(h); err != nil {
			return nil, err
		}
	}
	for i, c := range s.ClarificationPoints {
		if err := checkTrimmed(fmt.Sprintf("clarificationPoints[%d]", i), "locationCode", c.LocationCode, "handoffPointId", c.HandoffPointID); err != nil {
			return nil, err
		}
		p := Place{Kind: PlaceClarification, Code: c.LocationCode, HandoffPointID: c.HandoffPointID}
		if err := l.addAMRPlace(c.LocationCode, p); err != nil {
			return nil, err
		}
	}
	return l, nil
}

func checkHeader(s Spec) error {
	if blank(s.SiteID) {
		return invalid("siteId is required")
	}
	if blank(s.Fleet.FleetID) {
		return invalid("fleet.fleetId is required")
	}
	if s.Fleet.LegsPerRobotHour < 1 {
		return invalid("fleet.legsPerRobotHour must be at least 1")
	}
	if len(s.Asrs) == 0 {
		return invalid("at least one AS/RS is required")
	}
	if len(s.ClarificationPoints) == 0 {
		return invalid("at least one clarification point is required")
	}
	return checkTrimmed("layout", "siteId", s.SiteID, "fleet.fleetId", s.Fleet.FleetID)
}

func (l *Layout) addRef(ref string, p Place) error {
	if blank(ref) {
		return invalid("a location code is required (%s %s)", p.Kind, p.Code)
	}
	if _, dup := l.byRef[ref]; dup {
		return invalid("duplicate reference %q", ref)
	}
	l.byRef[ref] = p
	return nil
}

func (l *Layout) addHandoffPoint(id string, p Place) error {
	if blank(id) {
		return invalid("handoff point id is required (%s %s)", p.Kind, p.Code)
	}
	if _, dup := l.byHandoff[id]; dup {
		return invalid("duplicate handoff point %q", id)
	}
	l.byHandoff[id] = p
	return nil
}

func (l *Layout) addAMRPlace(ref string, p Place) error {
	if err := l.addHandoffPoint(p.HandoffPointID, p); err != nil {
		return err
	}
	return l.addRef(ref, p)
}

func (l *Layout) addAsrs(i int, a Asrs) error {
	if blank(a.AsrsID) || blank(a.LocationCode) {
		return invalid("an AS/RS needs asrsId and locationCode (asrs[%d], asrsId %q, locationCode %q)", i, a.AsrsID, a.LocationCode)
	}
	if err := checkTrimmed(fmt.Sprintf("asrs[%d]", i), "asrsId", a.AsrsID, "locationCode", a.LocationCode); err != nil {
		return err
	}
	if a.RetrievalsPerUnitHour < 1 {
		return invalid("AS/RS %s: retrievalsPerUnitHour must be at least 1", a.AsrsID)
	}
	if _, dup := l.asrs[a.AsrsID]; dup {
		return invalid("duplicate AS/RS %s", a.AsrsID)
	}
	if err := l.addRef(a.LocationCode, Place{Kind: PlaceAsrs, Code: a.LocationCode, AsrsID: a.AsrsID}); err != nil {
		return err
	}
	// The port kinds seen (addPort admits only INPUT and OUTPUT): one set checked for both kinds, so no branch
	// swaps two symmetric counters (Task 26 mutation work, behaviour unchanged).
	kinds := map[PortKind]bool{}
	for j, p := range a.Ports {
		if err := l.addPort(a.AsrsID, j, p); err != nil {
			return err
		}
		kinds[p.Kind] = true
	}
	if !kinds[PortInput] || !kinds[PortOutput] {
		return invalid("AS/RS %s needs at least one INPUT and one OUTPUT port", a.AsrsID)
	}
	l.asrs[a.AsrsID] = a
	return nil
}

func (l *Layout) addPort(asrsID string, j int, p Port) error {
	if blank(p.PortID) {
		return invalid("AS/RS %s: a port needs portId (ports[%d], locationCode %q)", asrsID, j, p.LocationCode)
	}
	entry := fmt.Sprintf("AS/RS %s ports[%d]", asrsID, j)
	if err := checkTrimmed(entry, "portId", p.PortID, "locationCode", p.LocationCode, "handoffPointId", p.HandoffPointID); err != nil {
		return err
	}
	if p.Kind != PortInput && p.Kind != PortOutput {
		return invalid("AS/RS %s port %s: kind must be INPUT or OUTPUT", asrsID, p.PortID)
	}
	key := asrsID + "/" + p.PortID
	if _, dup := l.ports[key]; dup {
		return invalid("duplicate port %s", key)
	}
	place := Place{Kind: PlacePort, Code: p.LocationCode, AsrsID: asrsID, PortID: p.PortID, PortKind: p.Kind, HandoffPointID: p.HandoffPointID}
	if err := l.addAMRPlace(p.LocationCode, place); err != nil {
		return err
	}
	l.ports[key] = place
	if p.Kind == PortInput {
		l.inputs[asrsID] = append(l.inputs[asrsID], place)
	}
	return nil
}

func (l *Layout) addStation(i int, st Station) error {
	if blank(st.StationID) {
		return invalid("a station needs stationId (stations[%d], locationCode %q)", i, st.LocationCode)
	}
	entry := fmt.Sprintf("stations[%d]", i)
	if err := checkTrimmed(entry, "stationId", st.StationID, "locationCode", st.LocationCode, "handoffPointId", st.HandoffPointID); err != nil {
		return err
	}
	if st.BufferLimit < 1 {
		return invalid("station %s: bufferLimit must be at least 1", st.StationID)
	}
	p := Place{Kind: PlaceStation, Code: st.StationID, StationID: st.StationID, HandoffPointID: st.HandoffPointID}
	if err := l.addAMRPlace(st.StationID, p); err != nil {
		return err
	}
	if !blank(st.LocationCode) {
		if err := l.addRef(st.LocationCode, p); err != nil {
			return err
		}
	}
	l.stations[st.StationID] = st
	return nil
}

func (l *Layout) addHandoff(h Handoff) error {
	switch h.Kind {
	case HandoffDecant, HandoffPack, HandoffDock, HandoffForward, HandoffOther:
	default:
		return invalid("handoff %s: kind must be DECANT, PACK, DOCK, FORWARD or OTHER", h.LocationCode)
	}
	p := Place{Kind: PlaceHandoff, Code: h.LocationCode, HandoffPointID: h.HandoffPointID, HandoffKind: h.Kind}
	return l.addAMRPlace(h.LocationCode, p)
}
