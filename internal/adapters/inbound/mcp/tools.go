package mcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

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
			"the containers waiting upstream in admission order (priority, then due time). Read-only.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closedWorld},
		Meta:        authorityMeta(authorityL1)}, d.getStation)
	addTool(server, &mcp.Tool{Name: "list_subsystems",
		Description: "List the capacity of the AMR fleet, the AS/RS and every station as SubsystemCapacityChanged publishes it " +
			"(status, units total and available, open work, capacity per hour, queue depth). Read-only.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closedWorld},
		Meta:        authorityMeta(authorityL1)}, d.listSubsystems)
	addTool(server, &mcp.Tool{Name: "list_equipment_units",
		Description: "List the last reported state of every shuttle-and-lift unit of one AS/RS (IDLE, EXECUTE, HELD), as " +
			"asrs-control's EquipmentStateChanged reported it. Read-only.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closedWorld},
		Meta:        authorityMeta(authorityL1)}, d.listUnits)
}

func (d Deps) getTransport(ctx context.Context, in transportInput) (transportPayload, error) {
	v, err := d.Reads.Transport(ctx, in.TransportRequestID)
	if err != nil {
		return transportPayload{}, mapError(ctx, err)
	}
	var o transportorder.Snapshot
	if v.OrderID != "" {
		if o, err = d.Reads.OrderDetail(ctx, in.TransportRequestID); err != nil {
			return transportPayload{}, mapError(ctx, err)
		}
	}
	return toTransport(v, o), nil
}

func (d Deps) getTrack(ctx context.Context, in containerInput) (trackPayload, error) {
	v, err := d.Reads.Track(ctx, in.ContainerID)
	if err != nil {
		return trackPayload{}, mapError(ctx, err)
	}
	return toTrack(v), nil
}

func (d Deps) getStation(ctx context.Context, in stationInput) (stationPayload, error) {
	s, err := d.Reads.Station(ctx, in.StationID)
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
	us, err := d.Reads.EquipmentUnits(ctx, in.AsrsID)
	if err != nil {
		return unitsPayload{}, mapError(ctx, err)
	}
	return toUnits(in.AsrsID, us), nil
}

func addTool[In, Out any](server *mcp.Server, tool *mcp.Tool, handle func(context.Context, In) (Out, error)) {
	mcp.AddTool(server, tool, func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		out, err := handle(ctx, in)
		if err != nil {
			var zero Out
			return nil, zero, err
		}
		return nil, out, nil
	})
}
