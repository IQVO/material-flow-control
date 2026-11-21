package layout

import (
	"sort"
	"strings"
)

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
