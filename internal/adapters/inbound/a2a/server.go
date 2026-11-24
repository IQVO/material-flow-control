// Package a2a is material-flow-control's inbound Agent2Agent adapter: a third driving adapter over the
// SAME read queries the REST and MCP adapters serve, through the usecases.Reader (ADR 0007), built on the
// official A2A Go SDK (a2aproject/a2a-go/v2), JSON-RPC 2.0 over HTTP, with the Agent Card at the
// well-known path.
//
// A2A is the DELEGATION channel between agents: a client sends a goal-shaped skill request and this agent
// answers from its own state through its own read queries. It is not a data-sync channel: facts still
// travel only as CloudEvents on Kafka through the outbox. The agent is READ-ONLY like the MCP server: its
// five skills read a transport's status, a container's position, a station queue, the subsystem capacity
// and the AS/RS unit states. Requesting, cancelling and resolving a clarification stay on REST. No
// outbound A2A client lives here, and no auth of any kind (fleet decision 2026-09-11). Equipment ids
// (robot keys, storage job and leg ids) appear here and in the MCP view only, never on relationship 1.
package a2a

import (
	"context"
	"iter"
	"net/http"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

	"github.com/IQVO/material-flow-control/internal/application/facts"
	"github.com/IQVO/material-flow-control/internal/application/usecases"
	"github.com/IQVO/material-flow-control/internal/domain/capacity"
	"github.com/IQVO/material-flow-control/internal/domain/stationqueue"
	"github.com/IQVO/material-flow-control/internal/domain/transportorder"
)

// Config is the deployment identity of the agent: the absolute URL clients dial (advertised on the card;
// in-cluster http://<release>-a2a.<ns>.svc.cluster.local:8093/) and the card version.
type Config struct {
	AgentURL string
	Version  string
}

// Reads is the read side the skills need, the same method set as the MCP adapter's (*usecases.Reader
// implements it). Nothing here can write (ADR 0007); cmd/a2a wires no write seam.
type Reads interface {
	Transport(ctx context.Context, requestID string) (usecases.TransportView, error)
	OrderDetail(ctx context.Context, requestID string) (transportorder.Snapshot, error)
	Track(ctx context.Context, containerID string) (usecases.TrackView, error)
	Station(ctx context.Context, stationID string) (stationqueue.Snapshot, error)
	Subsystems(ctx context.Context) ([]capacity.Snapshot, error)
	EquipmentUnits(ctx context.Context, asrsID string) ([]facts.EquipmentState, error)
}

// Deps is everything the skills need, injected by the composition root.
type Deps struct {
	Reads Reads
}

// cardDescription is what an agent client is told about this agent.
const cardDescription = "material-flow-control (read-only): transport status, container position, station queues, subsystem " +
	"capacity and AS/RS unit states of the WCS material flow controller. Authority L1: it reads, it never moves a container."

// Card is the Agent Card: five curated, goal-shaped READ skills, each tagged authority:L1 (Agentic Standard
// v2) like the MCP tools they mirror. No security schemes on purpose: unauthenticated by the same fleet
// decision as REST and MCP.
func Card(cfg Config) *a2a.AgentCard {
	return &a2a.AgentCard{
		Name:                "material-flow-control",
		Description:         cardDescription,
		Version:             cfg.Version,
		Provider:            &a2a.AgentProvider{Org: "IQVO", URL: "https://github.com/IQVO/material-flow-control"},
		SupportedInterfaces: []*a2a.AgentInterface{a2a.NewAgentInterface(cfg.AgentURL, a2a.TransportProtocolJSONRPC)},
		DefaultInputModes:   []string{contentTypeJSON, "text/plain"},
		DefaultOutputModes:  []string{contentTypeJSON, "text/plain"},
		Capabilities:        a2a.AgentCapabilities{Streaming: false},
		Skills: []a2a.AgentSkill{
			{
				ID:   skillTransportStatus,
				Name: "Transport status",
				Description: "Answer one transport request: status (IN_PROGRESS, CANCEL_REQUESTED, COMPLETED, FAILED, REJECTED), " +
					"reason, the container's last confirmed location and every leg with its subsystem, kind (RETRIEVE, MOVE, " +
					"STORE), status, the subsystem's job or leg id and the robot. Arguments: transportRequestId (required). " +
					"Answers a transport-request-not-found problem for an unknown id and a transport-request-expired " +
					"problem for an id whose ended order the retention purged. Persists nothing.",
				Tags:       []string{"transport", "read", "authority:L1"},
				InputModes: []string{contentTypeJSON},
				Examples:   []string{`{"skill":"transport_status","arguments":{"transportRequestId":"task-1.1"}}`},
			},
			{
				ID:   skillContainerPosition,
				Name: "Container position",
				Description: "Answer where a container physically is (IN_ASRS, AT_PORT, ON_ROBOT with the robot, AT_STATION, " +
					"AT_HANDOFF, AT_CLARIFICATION, UNKNOWN), its active transport and its clarification reason. Arguments: " +
					"containerId (required). Answers a container-track-not-found problem for an unknown container. Persists nothing.",
				Tags:       []string{"container", "read", "authority:L1"},
				InputModes: []string{contentTypeJSON},
				Examples:   []string{`{"skill":"container_position","arguments":{"containerId":"TOTE-1"}}`},
			},
			{
				ID:   skillStationQueue,
				Name: "Station queue",
				Description: "Answer a goods-to-person station queue: buffer limit, the containers occupying it in position " +
					"order and the containers waiting upstream in admission order (priority, then due time). Arguments: " +
					"stationId (required). Answers a station-not-found problem for an unknown station. Persists nothing.",
				Tags:       []string{"station", "read", "authority:L1"},
				InputModes: []string{contentTypeJSON},
				Examples:   []string{`{"skill":"station_queue","arguments":{"stationId":"ST1"}}`},
			},
			{
				ID:   skillSubsystemCapacity,
				Name: "Subsystem capacity",
				Description: "Answer the capacity of the AMR fleet, the AS/RS and every station as SubsystemCapacityChanged " +
					"publishes it (status, units total and available, open work, capacity per hour, queue depth). No " +
					"arguments. Persists nothing.",
				Tags:       []string{"capacity", "read", "authority:L1"},
				InputModes: []string{contentTypeJSON},
				Examples:   []string{`{"skill":"subsystem_capacity","arguments":{}}`},
			},
			{
				ID:   skillEquipmentUnits,
				Name: "AS/RS unit states",
				Description: "Answer the last reported state of every shuttle-and-lift unit of one AS/RS (IDLE, EXECUTE, HELD), " +
					"as asrs-control's EquipmentStateChanged reported it. Arguments: asrsId (required). Answers an asrs-not-found " +
					"problem for an AS/RS the layout does not name. Persists nothing.",
				Tags:       []string{"asrs", "equipment", "read", "authority:L1"},
				InputModes: []string{contentTypeJSON},
				Examples:   []string{`{"skill":"equipment_units","arguments":{"asrsId":"ASRS1"}}`},
			},
		},
	}
}

// NewExecutor maps an incoming message to a skill and answers with ONE agent Message (a message-only
// agent): every skill is a request/response read, so there is no Task lifecycle. The answer carries the
// caller's contextId so a delegating saga can group it, and no taskId (carry, Task 21 review: no Task exists).
func NewExecutor(deps Deps) a2asrv.AgentExecutor {
	return a2asrv.AgentExecutorFunc(func(ctx context.Context, ec *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
		return func(yield func(a2a.Event, error) bool) {
			yield(answer(ctx, deps, ec), nil)
		}
	})
}

// Handler wraps the executor in the JSON-RPC 2.0 HTTP handler, the only transport shipped.
func Handler(executor a2asrv.AgentExecutor) http.Handler {
	return a2asrv.NewJSONRPCHandler(a2asrv.NewHandler(executor))
}
