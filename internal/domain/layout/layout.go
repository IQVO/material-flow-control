// Package layout is the material-flow layout (ADR 0002): the AS/RS systems with their ports, the AMR fleet, the
// goods-to-person stations with their buffer limits (ADR 0001), the other handoffs and the clarification points,
// and the places a transport request can name.
package layout

import (
	"errors"
	"fmt"
	"sort"
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

// carry (Task 2 review): an id that is only whitespace is missing.
func blank(id string) bool { return strings.TrimSpace(id) == "" }

// carry (Task 2 review): New keeps a deep copy of the caller's Spec, so a Layout is immutable after New.
func cloneSpec(s Spec) Spec {
	s.Asrs = append([]Asrs(nil), s.Asrs...)
	for i := range s.Asrs {
		s.Asrs[i].Ports = append([]Port(nil), s.Asrs[i].Ports...)
	}
	s.Stations = append([]Station(nil), s.Stations...)
	s.Handoffs = append([]Handoff(nil), s.Handoffs...)
	s.ClarificationPoints = append([]ClarificationPoint(nil), s.ClarificationPoints...)
	return s
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
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
	for _, h := range s.Handoffs {
		if err := l.addHandoff(h); err != nil {
			return nil, err
		}
	}
	for _, c := range s.ClarificationPoints {
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
	return nil
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
	if a.RetrievalsPerUnitHour < 1 {
		return invalid("AS/RS %s: retrievalsPerUnitHour must be at least 1", a.AsrsID)
	}
	if _, dup := l.asrs[a.AsrsID]; dup {
		return invalid("duplicate AS/RS %s", a.AsrsID)
	}
	if err := l.addRef(a.LocationCode, Place{Kind: PlaceAsrs, Code: a.LocationCode, AsrsID: a.AsrsID}); err != nil {
		return err
	}
	in, out := 0, 0
	for j, p := range a.Ports {
		if err := l.addPort(a.AsrsID, j, p); err != nil {
			return err
		}
		if p.Kind == PortInput {
			in++
		} else {
			out++
		}
	}
	if in == 0 || out == 0 {
		return invalid("AS/RS %s needs at least one INPUT and one OUTPUT port", a.AsrsID)
	}
	l.asrs[a.AsrsID] = a
	return nil
}

func (l *Layout) addPort(asrsID string, j int, p Port) error {
	if blank(p.PortID) {
		return invalid("AS/RS %s: a port needs portId (ports[%d], locationCode %q)", asrsID, j, p.LocationCode)
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

// SiteID is the site the layout describes.
func (l *Layout) SiteID() string { return l.spec.SiteID }

// Fleet is the AMR fleet.
func (l *Layout) Fleet() Fleet { return l.spec.Fleet }

// Resolve finds the place a request names: any location code, or a station id.
func (l *Layout) Resolve(ref string) (Place, bool) {
	p, ok := l.byRef[ref]
	return p, ok
}

// ByHandoffPoint finds the place of an amr-fleet-control handoff point.
func (l *Layout) ByHandoffPoint(id string) (Place, bool) {
	p, ok := l.byHandoff[id]
	return p, ok
}

// Port finds an AS/RS port by its asrs-control ids.
func (l *Layout) Port(asrsID, portID string) (Place, bool) {
	p, ok := l.ports[asrsID+"/"+portID]
	return p, ok
}

// AsrsPlace is the logical location of an AS/RS.
func (l *Layout) AsrsPlace(asrsID string) (Place, bool) {
	a, ok := l.asrs[asrsID]
	if !ok {
		return Place{}, false
	}
	return l.byRef[a.LocationCode], true
}

// Asrs returns one AS/RS as configured, with its own copy of the ports.
func (l *Layout) Asrs(asrsID string) (Asrs, bool) {
	a, ok := l.asrs[asrsID]
	a.Ports = append([]Port(nil), a.Ports...) // carry (Task 2 review): never hand out the layout's slice
	return a, ok
}

// AsrsIDs lists the AS/RS ids in ascending order.
func (l *Layout) AsrsIDs() []string {
	ids := make([]string, 0, len(l.asrs))
	for id := range l.asrs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Station returns one station as configured.
func (l *Layout) Station(id string) (Station, bool) {
	st, ok := l.stations[id]
	return st, ok
}

// Stations lists the stations in ascending id order.
func (l *Layout) Stations() []Station {
	out := make([]Station, 0, len(l.stations))
	for _, st := range l.stations {
		out = append(out, st)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StationID < out[j].StationID })
	return out
}

// ClarificationPoint is the first configured clarification point (ADR 0002: containers go there).
func (l *Layout) ClarificationPoint() Place {
	c := l.spec.ClarificationPoints[0]
	return l.byRef[c.LocationCode]
}

// InputPorts lists an AS/RS's input ports in layout order.
func (l *Layout) InputPorts(asrsID string) []Place {
	return append([]Place(nil), l.inputs[asrsID]...)
}
