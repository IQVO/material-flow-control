package mcp

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/IQVO/material-flow-control/internal/application/facts"
	"github.com/IQVO/material-flow-control/internal/application/usecases"
	"github.com/IQVO/material-flow-control/internal/domain/capacity"
	"github.com/IQVO/material-flow-control/internal/domain/stationqueue"
	"github.com/IQVO/material-flow-control/internal/domain/transportorder"
)

// Reads is the read side (*usecases.Reader implements it); the adapter holds nothing that can write (ADR 0007).
type Reads interface {
	Transport(ctx context.Context, requestID string) (usecases.TransportView, error)
	OrderDetail(ctx context.Context, requestID string) (transportorder.Snapshot, error)
	Track(ctx context.Context, containerID string) (usecases.TrackView, error)
	Station(ctx context.Context, stationID string) (stationqueue.Snapshot, error)
	Subsystems(ctx context.Context) ([]capacity.Snapshot, error)
	EquipmentUnits(ctx context.Context, asrsID string) ([]facts.EquipmentState, error)
}

// Deps is everything the MCP surface needs, injected by the composition root.
type Deps struct {
	Reads Reads
}

type noInput struct{}

type transportInput struct {
	TransportRequestID string `json:"transportRequestId" jsonschema:"the WES transport_request_id, e.g. task-1.1"`
}

type containerInput struct {
	ContainerID string `json:"containerId" jsonschema:"the container id (LPN), e.g. TOTE-1"`
}

type stationInput struct {
	StationID string `json:"stationId" jsonschema:"the goods-to-person station id of the layout, e.g. ST1"`
}

type asrsInput struct {
	AsrsID string `json:"asrsId" jsonschema:"the AS/RS id of the layout, e.g. ASRS1"`
}

// registerTools adds the five read-only tools, each annotated read-only, idempotent, closed-world, authority L1
// (the literal inlined per tool so the static authority sensor proves ReadOnlyHint). TestToolSurface pins the set.
func (d Deps) registerTools(server *mcp.Server) {
	closedWorld := false
	addTool(server, &mcp.Tool{Name: "get_transport",
		Description: "Read one transport request: status (IN_PROGRESS, CANCEL_REQUESTED, COMPLETED, FAILED, REJECTED), reason, " +
			"the container's last confirmed location, and every leg with its subsystem, kind (RETRIEVE, MOVE, STORE), status, " +
			"the subsystem's job or leg id and the robot. Read-only; an unknown id fails with transport-request-not-found.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closedWorld},
		Meta:        authorityMeta(authorityL1)}, d.getTransport)
	addTool(server, &mcp.Tool{Name: "get_container_track",
		Description: "Read where a container physically is (IN_ASRS, AT_PORT, ON_ROBOT with the robot, AT_STATION, AT_HANDOFF, " +
			"AT_CLARIFICATION, UNKNOWN), its active transport and its clarification reason. Read-only; an unknown container fails " +
			"with container-track-not-found.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closedWorld},
		Meta:        authorityMeta(authorityL1)}, d.getTrack)
	addTool(server, &mcp.Tool{Name: "get_station",
		Description: "Read a goods-to-person station queue: buffer limit, the containers occupying it in position order and " +
			"the containers waiting upstream in admission order (priority, then due time). Read-only; an unknown station fails with " +
			"station-not-found.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closedWorld},
		Meta:        authorityMeta(authorityL1)}, d.getStation)
	addTool(server, &mcp.Tool{Name: "list_subsystems",
		Description: "List the capacity of the AMR fleet, the AS/RS and every station as SubsystemCapacityChanged publishes it " +
			"(status, units total and available, open work, capacity per hour, queue depth). Read-only.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closedWorld},
		Meta:        authorityMeta(authorityL1)}, d.listSubsystems)
	addTool(server, &mcp.Tool{Name: "list_equipment_units",
		Description: "List the last reported state of every shuttle-and-lift unit of one AS/RS (IDLE, EXECUTE, HELD), as " +
			"asrs-control's EquipmentStateChanged reported it. Read-only; an AS/RS the layout does not name fails with asrs-not-found.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closedWorld},
		Meta:        authorityMeta(authorityL1)}, d.listUnits)
}

func (d Deps) getTransport(ctx context.Context, in transportInput) (transportPayload, error) {
	id, err := requiredID("transportRequestId", in.TransportRequestID)
	if err != nil {
		return transportPayload{}, err
	}
	v, err := d.Reads.Transport(ctx, id)
	if err != nil {
		return transportPayload{}, mapError(ctx, err)
	}
	var o transportorder.Snapshot
	if v.OrderID != "" {
		if o, err = d.Reads.OrderDetail(ctx, id); err != nil {
			return transportPayload{}, mapError(ctx, err)
		}
	}
	return toTransport(v, o), nil
}

func (d Deps) getTrack(ctx context.Context, in containerInput) (trackPayload, error) {
	id, err := requiredID("containerId", in.ContainerID)
	if err != nil {
		return trackPayload{}, err
	}
	v, err := d.Reads.Track(ctx, id)
	if err != nil {
		return trackPayload{}, mapError(ctx, err)
	}
	return toTrack(v), nil
}

func (d Deps) getStation(ctx context.Context, in stationInput) (stationPayload, error) {
	id, err := requiredID("stationId", in.StationID)
	if err != nil {
		return stationPayload{}, err
	}
	s, err := d.Reads.Station(ctx, id)
	if err != nil {
		return stationPayload{}, mapError(ctx, err)
	}
	return toStation(s), nil
}

func (d Deps) listSubsystems(ctx context.Context, _ noInput) (subsystemsPayload, error) {
	snaps, err := d.Reads.Subsystems(ctx)
	if err != nil {
		return subsystemsPayload{}, mapError(ctx, err)
	}
	return toSubsystems(snaps), nil
}

func (d Deps) listUnits(ctx context.Context, in asrsInput) (unitsPayload, error) {
	id, err := requiredID("asrsId", in.AsrsID)
	if err != nil {
		return unitsPayload{}, err
	}
	us, err := d.Reads.EquipmentUnits(ctx, id)
	if err != nil {
		return unitsPayload{}, mapError(ctx, err)
	}
	return toUnits(id, us), nil
}

// tracerName is the instrumentation scope of the tool spans ("mcp.tool <name>").
const tracerName = "github.com/IQVO/material-flow-control/internal/adapters/inbound/mcp"

// addTool registers handle under tool. carry (Task 20 review): every call runs in one "mcp.tool <name>" span
// carrying gen_ai.operation.name=execute_tool, the tool name and its declared authority (warehouse.authority);
// a failed call records the tool error (the mapped one: no DSN or SQL reaches a trace) and marks the span Error.
func addTool[In, Out any](server *mcp.Server, tool *mcp.Tool, handle func(context.Context, In) (Out, error)) {
	level, _ := tool.Meta[authorityMetaKey].(string)
	name := tool.Name
	mcp.AddTool(server, tool, func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		ctx, span := otel.Tracer(tracerName).Start(ctx, "mcp.tool "+name, trace.WithAttributes(
			attribute.String("gen_ai.operation.name", "execute_tool"), attribute.String("gen_ai.tool.name", name),
			attribute.String("warehouse.authority", level)))
		defer span.End()
		out, err := handle(ctx, in)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			var zero Out
			return nil, zero, err
		}
		return nil, out, nil
	})
}

// requiredID is the agent surfaces' one argument rule (the A2A skills' `required`): the id is trimmed, and blank after
// trimming is invalid-argument. carry (Task 21 review)
func requiredID(name, v string) (string, error) {
	if v = strings.TrimSpace(v); v == "" {
		return "", toolError("invalid-argument", name+" is required")
	}
	return v, nil
}
