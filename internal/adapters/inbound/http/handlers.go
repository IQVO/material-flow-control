package http

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/IQVO/material-flow-control/internal/application/usecases"
)

// pathID reads a path identifier: 1-128 characters, no control characters.
func pathID(r *http.Request, name string) (string, error) {
	v := chi.URLParam(r, name)
	if v == "" || utf8.RuneCountInString(v) > maxIDLen {
		return "", errInvalidParameter
	}
	if err := rejectNUL(name, v); err != nil {
		return "", errInvalidParameter
	}
	return v, nil
}

func (s *Server) handleRequestTransport(w http.ResponseWriter, r *http.Request) {
	var body transportRequestBody
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	req, err := body.request()
	if err != nil {
		writeError(w, r, err)
		return
	}
	view, err := s.Service.RequestTransport(r.Context(), req)
	var rejected *usecases.RejectedError
	if errors.As(err, &rejected) {
		writeRejection(w, r, rejected)
		return
	}
	if err != nil {
		writeError(w, r, err)
		return
	}
	w.Header().Set("Location", "/transport-requests/"+view.RequestID)
	writeJSON(w, http.StatusAccepted, toTransport(view))
}

// writeRejection answers a rejection: 422 with `reason` and `transport_request_id`, replay class OUTCOME (the
// rejection record and TransportRejected commit with the stored answer).
func writeRejection(w http.ResponseWriter, r *http.Request, rej *usecases.RejectedError) {
	p := rejectionProblem(rej.Reason)
	if m, ok := w.(keepMarker); ok {
		m.keepWrites()
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(p.status)
	_ = json.NewEncoder(w).Encode(struct {
		problemDetail
		Reason             string `json:"reason"`
		TransportRequestID string `json:"transport_request_id"`
	}{problemDetail{Type: problemBaseURI + p.slug, Title: p.title, Status: p.status, Code: p.slug, Detail: rej.Detail,
		Instance: r.URL.EscapedPath()}, string(rej.Reason), rej.RequestID})
}

func (s *Server) handleGetTransport(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "transportRequestId")
	if err != nil {
		writeError(w, r, err)
		return
	}
	view, err := s.Service.Transport(r.Context(), id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toTransport(view))
}

func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "transportRequestId")
	if err != nil {
		writeError(w, r, err)
		return
	}
	view, outcome, err := s.Service.CancelTransport(r.Context(), id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	status := http.StatusOK
	if outcome == usecases.CancelAsked {
		status = http.StatusAccepted
	}
	writeJSON(w, status, toTransport(view))
}

func (s *Server) handleGetTrack(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "containerId")
	if err != nil {
		writeError(w, r, err)
		return
	}
	view, err := s.Service.Track(r.Context(), id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toTrack(view))
}

func (s *Server) handleResolve(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "containerId")
	if err != nil {
		writeError(w, r, err)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
	if err != nil {
		writeError(w, r, errMalformedRequest)
		return
	}
	var body resolutionBody
	if len(bytes.TrimSpace(raw)) > 0 {
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		// carry (Task 18 review): one JSON value, nothing after it (decodeJSON's rule).
		if err := dec.Decode(&body); err != nil || dec.More() {
			writeError(w, r, errMalformedRequest)
			return
		}
	}
	view, err := s.Service.ResolveClarification(r.Context(), id, body.Location)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toTrack(view))
}

func (s *Server) handleGetStation(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "stationId")
	if err != nil {
		writeError(w, r, err)
		return
	}
	snap, err := s.Service.Station(r.Context(), id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toStation(snap))
}

func (s *Server) handleSubsystems(w http.ResponseWriter, r *http.Request) {
	snaps, err := s.Service.Subsystems(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toSubsystems(snaps))
}
