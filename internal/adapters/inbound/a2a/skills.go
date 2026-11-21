package a2a

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/IQVO/material-flow-control/internal/application/usecases"
	"github.com/IQVO/material-flow-control/internal/domain/transportorder"
)

// tracerName is the instrumentation scope of the skill spans ("a2a.skill <id>", "a2a.skill unknown").
const tracerName = "github.com/IQVO/material-flow-control/internal/adapters/inbound/a2a"

// The skill ids on the Agent Card.
const (
	skillTransportStatus   = "transport_status"
	skillContainerPosition = "container_position"
	skillStationQueue      = "station_queue"
	skillSubsystemCapacity = "subsystem_capacity"
	skillEquipmentUnits    = "equipment_units"
)

// authorityL1 is the authority every skill declares (its card tag is authority:L1): a pure read.
const authorityL1 = "L1"

// contentTypeJSON is the input mode of every skill.
const contentTypeJSON = "application/json"

// skillRequest is the wire contract of every call: the first message part carries the skill id and its
// arguments, as a data part or a text part holding the same JSON. Strict on purpose.
type skillRequest struct {
	Skill     string          `json:"skill"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

type transportArgs struct {
	TransportRequestID string `json:"transportRequestId"`
}

type containerArgs struct {
	ContainerID string `json:"containerId"`
}

type stationArgs struct {
	StationID string `json:"stationId"`
}

type noArgs struct{}

type asrsArgs struct {
	AsrsID string `json:"asrsId"`
}

// answer maps one message to a skill and returns the reply (the whole executor body).
func answer(ctx context.Context, deps Deps, ec *a2asrv.ExecutorContext) a2a.Event {
	req, p := parseSkillRequest(ec)
	if p != nil {
		return reply(ec, envelope{Problem: p}, p.summary())
	}
	// carry (Task 20 review, MCP parity): one "a2a.skill <id>" span per run with the GenAI operation and the
	// authority every skill declares; a problem answer records the error and marks the span Error.
	// carry (Task 21 review): the span is named by a skill the card OFFERS, or "unknown"; the caller-chosen id
	// is never recorded on it (no unbounded span names, no attacker text in telemetry).
	label := spanSkill(req.Skill)
	ctx, span := otel.Tracer(tracerName).Start(ctx, "a2a.skill "+label, trace.WithAttributes(
		attribute.String("a2a.skill", label), attribute.String("gen_ai.operation.name", "execute_tool"),
		attribute.String("gen_ai.tool.name", label), attribute.String("warehouse.authority", authorityL1)))
	defer span.End()
	dto, p := run(ctx, deps, req)
	if p != nil {
		desc := p.Code + ": " + p.Detail
		if label == skillUnknown {
			desc = p.Code // the detail quotes the raw id
		}
		span.RecordError(errors.New(desc))
		span.SetStatus(codes.Error, desc)
		return reply(ec, envelope{Skill: req.Skill, Problem: p}, p.summary())
	}
	return reply(ec, envelope{Skill: req.Skill, Data: &dto}, dto.summary)
}

// skillUnknown labels the span of a skill id the card does not offer.
const skillUnknown = "unknown"

// spanSkill is id when the card offers it, else skillUnknown.
func spanSkill(id string) string {
	switch id {
	case skillTransportStatus, skillContainerPosition, skillStationQueue, skillSubsystemCapacity, skillEquipmentUnits:
		return id
	default:
		return skillUnknown
	}
}

func run(ctx context.Context, deps Deps, req skillRequest) (skillDTO, *problem) {
	switch req.Skill {
	case skillTransportStatus:
		return deps.transportStatus(ctx, req.Arguments)
	case skillContainerPosition:
		return deps.containerPosition(ctx, req.Arguments)
	case skillStationQueue:
		return deps.stationQueue(ctx, req.Arguments)
	case skillSubsystemCapacity:
		return deps.subsystemCapacity(ctx, req.Arguments)
	case skillEquipmentUnits:
		return deps.equipmentUnits(ctx, req.Arguments)
	default:
		return skillDTO{}, problemUnknownSkill(req.Skill)
	}
}

// required decodes raw strictly into dst and returns the trimmed value of its one required argument.
func required(raw json.RawMessage, dst any, name string, value func() string) (string, *problem) {
	if err := decodeStrict(raw, dst); err != nil {
		return "", problemInvalidArgument(fmt.Sprintf("arguments: %v", err))
	}
	v := strings.TrimSpace(value())
	if v == "" {
		return "", problemInvalidArgument(name + " is required")
	}
	return v, nil
}

func (d Deps) transportStatus(ctx context.Context, raw json.RawMessage) (skillDTO, *problem) {
	var args transportArgs
	id, p := required(raw, &args, "transportRequestId", func() string { return args.TransportRequestID })
	if p != nil {
		return skillDTO{}, p
	}
	v, err := d.Reads.Transport(ctx, id)
	if err != nil {
		return skillDTO{}, problemFromUseCase(ctx, err)
	}
	var o transportorder.Snapshot
	if v.OrderID != "" {
		if o, err = d.Reads.OrderDetail(ctx, id); err != nil {
			return skillDTO{}, problemFromUseCase(ctx, err)
		}
	}
	dto := toTransport(v, o)
	return skillDTO{value: dto, summary: summarizeTransport(dto, o)}, nil
}

func (d Deps) containerPosition(ctx context.Context, raw json.RawMessage) (skillDTO, *problem) {
	var args containerArgs
	id, p := required(raw, &args, "containerId", func() string { return args.ContainerID })
	if p != nil {
		return skillDTO{}, p
	}
	v, err := d.Reads.Track(ctx, id)
	if err != nil {
		return skillDTO{}, problemFromUseCase(ctx, err)
	}
	dto := toTrack(v)
	return skillDTO{value: dto, summary: summarizeTrack(dto)}, nil
}

func (d Deps) stationQueue(ctx context.Context, raw json.RawMessage) (skillDTO, *problem) {
	var args stationArgs
	id, p := required(raw, &args, "stationId", func() string { return args.StationID })
	if p != nil {
		return skillDTO{}, p
	}
	s, err := d.Reads.Station(ctx, id)
	if err != nil {
		return skillDTO{}, problemFromUseCase(ctx, err)
	}
	dto := toStation(s)
	return skillDTO{value: dto, summary: summarizeStation(dto)}, nil
}

func (d Deps) subsystemCapacity(ctx context.Context, raw json.RawMessage) (skillDTO, *problem) {
	if err := decodeStrict(raw, &noArgs{}); err != nil {
		return skillDTO{}, problemInvalidArgument(fmt.Sprintf("arguments: %v", err))
	}
	snaps, err := d.Reads.Subsystems(ctx)
	if err != nil {
		return skillDTO{}, problemFromUseCase(ctx, err)
	}
	dto := toSubsystems(snaps)
	return skillDTO{value: dto, summary: summarizeSubsystems(dto)}, nil
}

func (d Deps) equipmentUnits(ctx context.Context, raw json.RawMessage) (skillDTO, *problem) {
	var args asrsArgs
	id, p := required(raw, &args, "asrsId", func() string { return args.AsrsID })
	if p != nil {
		return skillDTO{}, p
	}
	us, err := d.Reads.EquipmentUnits(ctx, id)
	if err != nil {
		return skillDTO{}, problemFromUseCase(ctx, err)
	}
	dto := toUnits(id, us)
	return skillDTO{value: dto, summary: summarizeUnits(dto)}, nil
}

// reply is the agent Message of one run: a data part with the envelope and a text part with the one-line
// summary, bound to the caller's contextId only. carry (Task 21 review): no taskId; the SDK mints one for every
// run, but a message-only agent creates no Task, so naming it would point a client at a task that never existed.
func reply(ec *a2asrv.ExecutorContext, env envelope, summary string) a2a.Event {
	msg := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewDataPart(env), a2a.NewTextPart(summary))
	msg.ContextID = ec.ContextID
	return msg
}

// parseSkillRequest reads the request off the first part. A malformed delegation is answered as a problem
// in the envelope, never raised as a transport error.
func parseSkillRequest(ec *a2asrv.ExecutorContext) (skillRequest, *problem) {
	msg := ec.Message
	if msg == nil || len(msg.Parts) == 0 {
		return skillRequest{}, problemMalformedRequest("the message carries no parts")
	}
	raw, err := partJSON(msg.Parts[0])
	if err != nil {
		return skillRequest{}, problemMalformedRequest(fmt.Sprintf("the first message part is not a skill request: %v", err))
	}
	var req skillRequest
	if err := decodeStrict(raw, &req); err != nil {
		return skillRequest{}, problemMalformedRequest(fmt.Sprintf("the skill request is not valid: %v", err))
	}
	if req.Skill == "" {
		return skillRequest{}, problemMalformedRequest(`the skill request carries no "skill" field`)
	}
	return req, nil
}

func partJSON(part *a2a.Part) ([]byte, error) {
	switch content := part.Content.(type) {
	case a2a.Data:
		return json.Marshal(content.Value)
	case a2a.Text:
		return []byte(string(content)), nil
	default:
		return nil, errors.New("expected a data part (or a text part holding the skill JSON)")
	}
}

// useCaseProblems names each typed error a read can meet with the SAME code the REST adapter puts in its
// problem (internal/adapters/inbound/http/errors.go), so an agent sees one error vocabulary on every surface.
var useCaseProblems = []struct {
	target error
	p      problem
}{
	{usecases.ErrRequestNotFound, problem{Code: "transport-request-not-found", Title: "Transport request not found", Status: 404}},
	{usecases.ErrTrackNotFound, problem{Code: "container-track-not-found", Title: "Container track not found", Status: 404}},
	{usecases.ErrStationNotFound, problem{Code: "station-not-found", Title: "Station not found", Status: 404}},
	{usecases.ErrAsrsNotFound, problem{Code: "asrs-not-found", Title: "AS/RS not found", Status: 404}}, // carry (Task 20 review)
	{usecases.ErrInvalidRequest, problem{Code: "invalid-parameter", Title: "Invalid parameter", Status: 400}},
}

// problemFromUseCase maps a read error with the REST adapter's codes: a typed error keeps its code and
// message; anything else is logged and answered 500 with an adapter-owned detail (never the cause, so no
// DSN or SQL reaches an agent).
func problemFromUseCase(ctx context.Context, err error) *problem {
	for _, entry := range useCaseProblems {
		if errors.Is(err, entry.target) {
			return newProblem(entry.p.Code, entry.p.Title, entry.p.Status, err.Error())
		}
	}
	slog.ErrorContext(ctx, "a2a skill failed", "error", err)
	return newProblem("internal-error", "Internal server error", 500,
		"the skill failed; see the server logs (trace id in the response metadata)")
}
