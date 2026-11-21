package mcp

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// mimeTypeJSON is the MIME type of every resource this server serves.
const mimeTypeJSON = "application/json"

// stationURIPrefix and stationURITemplate are the ONE resource template this server offers (Agentic
// Standard v2, AS-3): a goods-to-person station queue, loadable without a tool call.
const (
	stationURIPrefix   = "warehouse://material-flow-control/stations/"
	stationURITemplate = stationURIPrefix + "{stationId}"
)

// registerResources adds the station queue resource template (a read; resources cannot write).
func (d Deps) registerResources(server *mcp.Server) {
	server.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: stationURITemplate,
		Name:        "station",
		Description: "One goods-to-person station queue: buffer limit, the containers occupying it and the containers waiting upstream.",
		MIMEType:    mimeTypeJSON,
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		stationID, ok := parseStationURI(req.Params.URI)
		if !ok {
			return nil, mcp.ResourceNotFoundError(req.Params.URI)
		}
		s, err := d.Reads.Station(ctx, stationID)
		if err != nil {
			return nil, mapError(ctx, err)
		}
		encoded, err := json.Marshal(toStation(s))
		if err != nil {
			return nil, mapError(ctx, err)
		}
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: req.Params.URI, MIMEType: mimeTypeJSON, Text: string(encoded)}}}, nil
	})
}

// parseStationURI reads <stationId> off warehouse://material-flow-control/stations/<s>.
func parseStationURI(uri string) (stationID string, ok bool) {
	rest, found := strings.CutPrefix(uri, stationURIPrefix)
	if !found || rest == "" || strings.Contains(rest, "/") {
		return "", false
	}
	return rest, true
}
