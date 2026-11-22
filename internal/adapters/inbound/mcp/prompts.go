package mcp

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// reviewTransportSOP is the operator standard operating procedure the review_transport prompt hands
// the model (Agentic Standard v2, AS-3): what to read, how to interpret it and what done means. It
// names tools but never calls them, and every tool it names is read-only (ADR 0007).
const reviewTransportSOP = `You are reviewing one transport request of material-flow-control, the WCS material flow controller. Use only the MCP tools of material-flow-control; never assume state, read it.

Procedure:
1. Call get_transport(transportRequestId = REQUEST). It is material-flow-control's own record: status (IN_PROGRESS, CANCEL_REQUESTED, COMPLETED, FAILED, REJECTED), reason, the container's last confirmed location and every leg with its subsystem, kind (RETRIEVE, MOVE, STORE), status, the subsystem's job or leg id and the robot.
2. Call get_container_track(containerId = the transport's container_id): where the container physically is (IN_ASRS, AT_PORT, ON_ROBOT with the robot, AT_STATION, AT_HANDOFF, AT_CLARIFICATION, UNKNOWN), its active transport and its clarification reason.
3. When the destination is a station, call get_station(stationId = that station): its buffer limit, the containers occupying it and the containers waiting upstream in admission order.
4. For subsystem context, call list_subsystems (capacity and status of the AMR fleet, the AS/RS and the stations) and, for an AS/RS leg, list_equipment_units(asrsId = the leg's subsystem_id).

Interpretation:
- Legs run one at a time: a MOVE starts only after the container was presented at the AS/RS output port, a STORE only after the robot delivered it at the input port.
- A leg WAITING_STATION waits for a free slot at its station: compare the station's occupied count with its buffer limit.
- A container AT_CLARIFICATION is held until an operator resolves it; nothing dispatches it meanwhile.
- A HELD AS/RS unit or an UNAVAILABLE subsystem explains a leg that is not progressing.

Report the leg that is waiting and why, in the context's language (transport, leg, container, station, subsystem). Requesting, cancelling a transport and resolving a clarification are REST operations of material-flow-control: nothing in this server can perform them, so do not promise them.

Done means: you have reported the transport's status and reason as recorded, the waiting leg and its cause, and where the container is.`

// registerPrompts adds the workflow prompt (read-only by construction: a prompt returns text).
func (d Deps) registerPrompts(server *mcp.Server) {
	server.AddPrompt(&mcp.Prompt{
		Name:        "review_transport",
		Description: "Standard operating procedure for reviewing one transport request: read it, its container and its station, and report the leg that is waiting and why.",
		Arguments: []*mcp.PromptArgument{
			{Name: "transportRequestId", Description: "the WES transport_request_id to review, e.g. task-1.1", Required: true},
		},
	}, func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		request := orPlaceholder(req.Params.Arguments["transportRequestId"], "<transportRequestId>")
		text := strings.ReplaceAll(reviewTransportSOP, "REQUEST", request)
		return &mcp.GetPromptResult{
			Description: "How to review a transport request: read it, its container and its station, report the waiting leg and its cause.",
			Messages:    []*mcp.PromptMessage{{Role: "user", Content: &mcp.TextContent{Text: text}}},
		}, nil
	})
}

func orPlaceholder(v, placeholder string) string {
	if v = strings.TrimSpace(v); v == "" {
		return placeholder
	}
	return v
}
