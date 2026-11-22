package mcp

import (
	"errors"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
)

// carry (Task 20 review): an unknown station read through the resource template is the protocol's
// resource-not-found (the SDK's ResourceNotFoundError: code -32602, data naming the URI), and its message keeps the REST slug station-not-found.
func TestAnUnknownStationResourceIsResourceNotFound(t *testing.T) {
	uri := "warehouse://material-flow-control/stations/ST9"
	_, err := readResource(t, Deps{Reads: fakeReads{}}, uri)
	var werr *jsonrpc.Error
	if !errors.As(err, &werr) || werr.Code != jsonrpc.CodeInvalidParams || !strings.Contains(werr.Message, "station-not-found") ||
		!strings.Contains(string(werr.Data), uri) {
		t.Fatalf("read %s = %#v (%v), want code %d, message with station-not-found, data naming the URI", uri, werr, err, jsonrpc.CodeInvalidParams)
	}
}
