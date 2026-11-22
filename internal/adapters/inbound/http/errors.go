package http

import (
	"errors"
	"net/http"

	"github.com/IQVO/material-flow-control/internal/application/usecases"
	"github.com/IQVO/material-flow-control/internal/domain/transportrequest"
)

const problemBaseURI = "https://errors.material-flow-control.warehouse-systems.dev/"

// replayClass is the idempotency class of a problem type (docs/idempotency-replay-classes.md).
type replayClass string

const (
	classFinal     replayClass = "FINAL"
	classOutcome   replayClass = "OUTCOME"
	classRetryable replayClass = "RETRYABLE"
)

type problem struct {
	status int
	slug   string
	title  string
	class  replayClass
}

var (
	errMalformedRequest = errors.New("request body is not valid JSON for this operation")
	errInvalidParameter = errors.New("parameter is invalid")
)

var (
	internalProblem        = problem{http.StatusInternalServerError, "internal-error", "Internal server error", classRetryable}
	dependencyUnavailable  = problem{http.StatusServiceUnavailable, "dependency-unavailable", "A runtime dependency is unavailable", classRetryable}
	idempotencyKeyRequired = problem{http.StatusBadRequest, "idempotency-key-required", "Idempotency-Key header is required", classFinal}
	idempotencyKeyInvalid  = problem{http.StatusBadRequest, "idempotency-key-invalid", "Idempotency-Key header is invalid", classFinal}
	idempotencyKeyReused   = problem{http.StatusConflict, "idempotency-key-reused", "Idempotency-Key was already used with a different request", classFinal}
	invalidParameter       = problem{http.StatusBadRequest, "invalid-parameter", "Parameter is invalid", classFinal}
)

// problemCatalogue maps every typed error to its problem, in match order. The four rejections are not here: a
// *usecases.RejectedError is answered by writeRejection with rejectionProblem (replay class OUTCOME).
var problemCatalogue = []struct {
	err error
	p   problem
}{
	{errMalformedRequest, problem{http.StatusBadRequest, "bad-request", "Malformed request", classFinal}},
	{errInvalidParameter, invalidParameter},
	{usecases.ErrInvalidRequest, invalidParameter},
	{usecases.ErrRequestConflict, problem{http.StatusConflict, "transport-request-conflict", "The transport_request_id was used with another request", classFinal}},
	// ruling P4-R12: RETRYABLE — the caller mints transport_request_id and a ContainerStored or an acceptance
	// creates a track, so the target of a keyed POST can exist by the retry.
	{usecases.ErrRequestNotFound, problem{http.StatusNotFound, "transport-request-not-found", "Transport request not found", classRetryable}},
	{usecases.ErrTrackNotFound, problem{http.StatusNotFound, "container-track-not-found", "Container track not found", classRetryable}},
	{usecases.ErrStationNotFound, problem{http.StatusNotFound, "station-not-found", "Station not found", classFinal}},
	{usecases.ErrUnknownLocation, problem{http.StatusUnprocessableEntity, "unknown-location", "The location is not a place of this automation", classFinal}},
	{usecases.ErrTransportEnded, problem{http.StatusConflict, "transport-ended", "The transport has ended", classRetryable}},
	{usecases.ErrNotCancellable, problem{http.StatusConflict, "transport-not-cancellable", "The container is on a robot or waiting on an AS/RS output port", classRetryable}},
	{usecases.ErrNotInClarification, problem{http.StatusConflict, "not-in-clarification", "The container is not in clarification", classRetryable}},
}

// rejectionProblem is the 422 of one of the four rejection reasons (seam decision 3).
func rejectionProblem(r transportrequest.Reason) problem {
	titles := map[transportrequest.Reason][2]string{
		transportrequest.ReasonUnknownContainer:       {"unknown-container", "The container is not known at the source"},
		transportrequest.ReasonContainerBusy:          {"container-busy", "The container has an active transport or is held at clarification"},
		transportrequest.ReasonDestinationUnreachable: {"destination-unreachable", "No route joins the source and the destination"},
		transportrequest.ReasonSubsystemUnavailable:   {"subsystem-unavailable", "A subsystem on the route is unavailable"},
	}
	t := titles[r]
	return problem{http.StatusUnprocessableEntity, t[0], t[1], classOutcome}
}

func problemFor(err error) problem {
	for _, e := range problemCatalogue {
		if errors.Is(err, e.err) {
			return e.p
		}
	}
	return internalProblem
}
