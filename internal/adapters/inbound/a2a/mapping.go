package a2a

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/IQVO/material-flow-control/internal/application/facts"
	"github.com/IQVO/material-flow-control/internal/application/usecases"
	"github.com/IQVO/material-flow-control/internal/domain/capacity"
	"github.com/IQVO/material-flow-control/internal/domain/stationqueue"
	"github.com/IQVO/material-flow-control/internal/domain/transportorder"
)

// problemBaseURI is the REST adapter's problem type base (internal/adapters/inbound/http/errors.go): a
// shared outcome carries the same type URI and code on every surface.
const problemBaseURI = "https://errors.material-flow-control.warehouse-systems.dev/"

// problem is the RFC 9457-shaped answer an agent client gets instead of a result: rejections and
// request-shape failures are business answers inside the envelope, not transport errors. Codes are the
// REST adapter's where the outcome is shared; the rest belong to the A2A request.
type problem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

func newProblem(code, title string, status int, detail string) *problem {
	return &problem{Type: problemBaseURI + code, Title: title, Status: status, Code: code, Detail: detail}
}

func (p *problem) summary() string { return fmt.Sprintf("%s (%d): %s", p.Title, p.Status, p.Detail) }

func problemMalformedRequest(detail string) *problem {
	return newProblem("malformed-skill-request", "Malformed skill request", 400, detail)
}

func problemUnknownSkill(skill string) *problem {
	return newProblem("unknown-skill", "Unknown skill", 400,
		fmt.Sprintf("skill %q is not offered by this agent; the Agent Card lists the supported skills", skill))
}

func problemInvalidArgument(detail string) *problem {
	return newProblem("invalid-argument", "Invalid skill argument", 400, detail)
}

// envelope is every answer's structured payload: exactly one of Data or Problem, plus the skill.
type envelope struct {
	Skill   string    `json:"skill"`
	Data    *skillDTO `json:"data,omitempty"`
	Problem *problem  `json:"problem,omitempty"`
}

// skillDTO is one skill's result: its payload (the MCP tool's shape, so an agent sees one vocabulary on both
// surfaces) marshalled as `data`, and the one-line summary of the text part.
type skillDTO struct {
	value   any
	summary string
}

func (d skillDTO) MarshalJSON() ([]byte, error) { return json.Marshal(d.value) }

// The payload shapes below re-declare the MCP adapter's (internal/adapters/inbound/mcp/payloads.go) field for
// field with identical json names; the adapters are siblings and never import each other.

type legDTO struct {
	Seq          int    `json:"seq"`
	Attempt      int    `json:"attempt"`
	Reference    string `json:"reference"`
	Subsystem    string `json:"subsystem"`
	SubsystemID  string `json:"subsystem_id"`
	Kind         string `json:"kind"`
	From         string `json:"from,omitempty"`
	To           string `json:"to,omitempty"`
	Status       string `json:"status"`
	SubsystemRef string `json:"subsystem_ref,omitempty"`
	Robot        string `json:"robot,omitempty"`
}

type transportDTO struct {
	TransportRequestID string     `json:"transport_request_id"`
	TransportOrderID   string     `json:"transport_order_id,omitempty"`
	ContainerID        string     `json:"container_id"`
	Purpose            string     `json:"purpose"`
	Status             string     `json:"status"`
	Reason             string     `json:"reason,omitempty"`
	SubsystemReason    string     `json:"subsystem_reason,omitempty"`
	ContainerLocation  string     `json:"container_location,omitempty"`
	LegsTotal          int        `json:"legs_total"`
	LegsDone           int        `json:"legs_done"`
	Retries            int        `json:"retries"`
	Legs               []legDTO   `json:"legs"`
	AcceptedAt         *time.Time `json:"accepted_at,omitempty"`
	EndedAt            *time.Time `json:"ended_at,omitempty"`
}

type positionDTO struct {
	Kind  string `json:"kind"`
	Code  string `json:"code,omitempty"`
	Robot string `json:"robot,omitempty"`
}

type trackDTO struct {
	ContainerID              string      `json:"container_id"`
	Position                 positionDTO `json:"position"`
	ActiveTransportRequestID string      `json:"active_transport_request_id,omitempty"`
	ClarificationReason      string      `json:"clarification_reason,omitempty"`
	UpdatedAt                time.Time   `json:"updated_at"`
	Moves                    int         `json:"moves"`
}

type stationDTO struct {
	StationID   string   `json:"station_id"`
	BufferLimit int      `json:"buffer_limit"`
	Occupied    int      `json:"occupied"`
	Occupants   []string `json:"occupants"`
	Waiting     []string `json:"waiting"`
}

type capacityDTO struct {
	Kind            string    `json:"subsystem_kind"`
	ID              string    `json:"subsystem_id"`
	Status          string    `json:"status"`
	UnitsTotal      int       `json:"units_total"`
	UnitsAvailable  int       `json:"units_available"`
	OpenWork        int       `json:"open_work"`
	CapacityPerHour int       `json:"capacity_per_hour"`
	QueueDepth      int       `json:"queue_depth"`
	MeasuredAt      time.Time `json:"measured_at"`
}

type subsystemsDTO struct {
	Subsystems []capacityDTO `json:"subsystems"`
}

type unitDTO struct {
	UnitID string    `json:"unit_id"`
	Aisle  int       `json:"aisle"`
	State  string    `json:"state"`
	At     time.Time `json:"changed_at"`
}

type unitsDTO struct {
	AsrsID string    `json:"asrs_id"`
	Units  []unitDTO `json:"units"`
}

func opt(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	u := t.UTC()
	return &u
}

func toTransport(v usecases.TransportView, o transportorder.Snapshot) transportDTO {
	out := transportDTO{TransportRequestID: v.RequestID, TransportOrderID: v.OrderID, ContainerID: v.ContainerID, Purpose: string(v.Purpose),
		Status: v.Status, Reason: v.Reason, SubsystemReason: o.SubsystemReason, ContainerLocation: v.ContainerLocation,
		LegsTotal: v.LegsTotal, LegsDone: v.LegsDone, Retries: o.Retries, Legs: []legDTO{}, AcceptedAt: opt(v.AcceptedAt), EndedAt: opt(v.EndedAt)}
	for _, l := range o.Legs {
		out.Legs = append(out.Legs, toLeg(l))
	}
	return out
}

func toLeg(l transportorder.Leg) legDTO {
	return legDTO{Seq: l.Seq, Attempt: l.Attempt, Reference: l.Ref, Subsystem: string(l.Subsystem), SubsystemID: l.SubsystemID,
		Kind: string(l.Kind), From: l.From.Code, To: l.To.Code, Status: string(l.Status), SubsystemRef: l.SubsystemRef, Robot: l.Robot}
}

func toTrack(v usecases.TrackView) trackDTO {
	s := v.Track
	out := trackDTO{ContainerID: s.ContainerID, Position: positionDTO{Kind: string(s.Position.Kind), Code: s.Position.Code,
		Robot: s.Position.Robot}, ActiveTransportRequestID: s.ActiveRequestID, UpdatedAt: s.UpdatedAt.UTC(), Moves: len(v.History)}
	if s.Clarification != nil {
		out.ClarificationReason = string(s.Clarification.Reason)
	}
	return out
}

func toStation(s stationqueue.Snapshot) stationDTO {
	q := stationqueue.Restore(s)
	out := stationDTO{StationID: s.StationID, BufferLimit: s.BufferLimit, Occupied: q.Occupied(), Occupants: []string{}, Waiting: []string{}}
	for _, o := range q.Occupants() {
		out.Occupants = append(out.Occupants, o.ContainerID)
	}
	for _, w := range q.Waiting() {
		out.Waiting = append(out.Waiting, w.ContainerID)
	}
	return out
}

func toSubsystems(snaps []capacity.Snapshot) subsystemsDTO {
	out := subsystemsDTO{Subsystems: []capacityDTO{}}
	for _, s := range snaps {
		out.Subsystems = append(out.Subsystems, capacityDTO{string(s.Kind), s.ID, string(s.Status), s.UnitsTotal, s.UnitsAvailable,
			s.OpenWork, s.CapacityPerHour, s.QueueDepth, s.MeasuredAt.UTC()})
	}
	return out
}

func toUnits(asrsID string, us []facts.EquipmentState) unitsDTO {
	out := unitsDTO{AsrsID: asrsID, Units: []unitDTO{}}
	for _, u := range us {
		out.Units = append(out.Units, unitDTO{UnitID: u.UnitID, Aisle: u.Aisle, State: u.State, At: u.At.UTC()})
	}
	return out
}

// summarizeTransport is the one-line answer, e.g. "task-1.1: IN_PROGRESS, leg 2 of 2 (MOVE on AMR1, robot
// EMU/R1, PICKED_UP)". The leg named is the first one that has not ended, else the last one.
func summarizeTransport(t transportDTO, o transportorder.Snapshot) string {
	s := t.TransportRequestID + ": " + t.Status
	if t.Reason != "" {
		s += " (" + t.Reason + ")"
	}
	if len(o.Legs) == 0 {
		return s
	}
	l, ok := transportorder.Restore(o).Current()
	if !ok {
		l = o.Legs[len(o.Legs)-1]
	}
	s += fmt.Sprintf(", leg %d of %d (%s on %s", l.Seq, t.LegsTotal, l.Kind, l.SubsystemID)
	if l.Robot != "" {
		s += ", robot " + l.Robot
	}
	return s + ", " + string(l.Status) + ")"
}

func summarizeTrack(t trackDTO) string {
	s := t.ContainerID + ": " + t.Position.Kind
	if t.Position.Code != "" {
		s += " " + t.Position.Code
	}
	if t.Position.Robot != "" {
		s += " (robot " + t.Position.Robot + ")"
	}
	if t.ActiveTransportRequestID != "" {
		s += ", transport " + t.ActiveTransportRequestID
	}
	if t.ClarificationReason != "" {
		s += ", in clarification (" + t.ClarificationReason + ")"
	}
	return s
}

func summarizeStation(s stationDTO) string {
	return fmt.Sprintf("%s: %d/%d occupied, %d waiting", s.StationID, s.Occupied, s.BufferLimit, len(s.Waiting))
}

func summarizeSubsystems(s subsystemsDTO) string {
	parts := make([]string, 0, len(s.Subsystems))
	for _, c := range s.Subsystems {
		parts = append(parts, fmt.Sprintf("%s/%s %s %d/%d", c.Kind, c.ID, c.Status, c.UnitsAvailable, c.UnitsTotal))
	}
	return fmt.Sprintf("%d subsystem(s): %s", len(parts), strings.Join(parts, ", "))
}

func summarizeUnits(u unitsDTO) string {
	parts := make([]string, 0, len(u.Units))
	for _, x := range u.Units {
		parts = append(parts, x.UnitID+" "+x.State)
	}
	return fmt.Sprintf("%s: %d unit(s): %s", u.AsrsID, len(parts), strings.Join(parts, ", "))
}
