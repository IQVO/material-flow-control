package layout

// PlaceKind says what a place is.
type PlaceKind string

const (
	// PlaceAsrs is the AS/RS logical location (one per AS/RS, seam decision 2).
	PlaceAsrs PlaceKind = "ASRS"
	// PlacePort is an AS/RS input or output port.
	PlacePort PlaceKind = "ASRS_PORT"
	// PlaceStation is a goods-to-person station.
	PlaceStation PlaceKind = "STATION"
	// PlaceHandoff is any other AMR place: decant, pack, dock, forward-area handoff.
	PlaceHandoff PlaceKind = "HANDOFF"
	// PlaceClarification is a clarification point.
	PlaceClarification PlaceKind = "CLARIFICATION"
)

// Place is one place a container can be at or be sent to. Code is its canonical reference: the AS/RS logical
// location code, a port's location code, a station id, or a handoff's or clarification point's location code.
// HandoffPointID is the amr-fleet-control handoff point where a robot picks or drops; it is empty for the AS/RS
// itself, which only asrs-control reaches.
type Place struct {
	Kind           PlaceKind
	Code           string
	AsrsID         string
	PortID         string
	PortKind       PortKind
	StationID      string
	HandoffPointID string
	HandoffKind    HandoffKind
}

// IsZero reports an unset place (a leg end resolved later, ADR 0002).
func (p Place) IsZero() bool { return p.Kind == "" }

// AMR reports whether a robot can pick at or drop at the place.
func (p Place) AMR() bool { return p.HandoffPointID != "" }
