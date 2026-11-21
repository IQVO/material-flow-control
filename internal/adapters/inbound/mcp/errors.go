package mcp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/IQVO/material-flow-control/internal/application/usecases"
)

// errorCatalogue names each typed error a read can meet with the SAME slug the REST adapter puts
// in its problem `type` (internal/adapters/inbound/http/errors.go), so a client sees one error
// vocabulary over both surfaces. Only the errors a READ can raise are listed.
var errorCatalogue = []struct {
	target error
	slug   string
}{
	{usecases.ErrRequestNotFound, "transport-request-not-found"},
	{usecases.ErrTrackNotFound, "container-track-not-found"},
	{usecases.ErrStationNotFound, "station-not-found"},
	{usecases.ErrAsrsNotFound, "asrs-not-found"}, // carry (Task 20 review); asrs-control's slug for the same outcome
	{usecases.ErrInvalidRequest, "invalid-parameter"},
}

// slugFor returns the REST slug of err; ok is false for an untyped error.
func slugFor(err error) (string, bool) {
	for _, entry := range errorCatalogue {
		if errors.Is(err, entry.target) {
			return entry.slug, true
		}
	}
	return "", false
}

// toolError builds "<slug>: <detail>". Returned from a handler it becomes an isError tool result,
// never a transport failure.
func toolError(slug, detail string) error {
	return fmt.Errorf("%s: %s", slug, detail)
}

// mapError turns a use-case or repository error into the tool error the model sees. Typed errors
// keep their slug and message; anything else is logged and reported generically, so
// infrastructure details (DSNs, SQL) never reach the model.
func mapError(ctx context.Context, err error) error {
	if slug, ok := slugFor(err); ok {
		return toolError(slug, err.Error())
	}
	slog.ErrorContext(ctx, "mcp tool failed with an unexpected error", "error", err)
	return toolError("internal-error", "an unexpected internal error occurred")
}
