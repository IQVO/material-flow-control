package http

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"
)

// maxBodyBytes bounds a request body; every body here is a few hundred bytes.
const maxBodyBytes = 64 << 10

// maxIDLen is the bound of every identifier in a path or a body (OpenAPI maxLength).
const maxIDLen = 128

type statusBody struct {
	Status string `json:"status"`
}

// problemDetail is the RFC 9457 body; Code is the fleet's stable error code.
type problemDetail struct {
	Type     string `json:"type"`
	Title    string `json:"title"`
	Status   int    `json:"status"`
	Code     string `json:"code"`
	Detail   string `json:"detail,omitempty"`
	Instance string `json:"instance"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// decodeJSON strictly decodes the body: empty, unknown fields, trailing data and bad JSON are
// errMalformedRequest.
func decodeJSON(r *http.Request, dest any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dest); err != nil {
		return fmt.Errorf("%w: %w", errMalformedRequest, err)
	}
	if !atEOF(dec) {
		return fmt.Errorf("%w: trailing data after the JSON body", errMalformedRequest)
	}
	return nil
}

// atEOF reports whether dec holds nothing after the value it decoded: the next Decode must be io.EOF, so a second
// value and a stray closer (`}` or `]`, which dec.More misses) are both trailing data. carry (Task 26 review)
func atEOF(dec *json.Decoder) bool { return errors.Is(dec.Decode(&struct{}{}), io.EOF) }

// rejectNUL refuses an identifier Postgres cannot store or a log line could be forged with:
// empty, longer than 128 characters, or holding NUL or any other control character.
func rejectNUL(field, v string) error {
	if v == "" || utf8.RuneCountInString(v) > maxIDLen || strings.ContainsFunc(v, unicode.IsControl) {
		return fmt.Errorf("%w: %s must be 1 to 128 characters without control characters", errInvalidParameter, field)
	}
	return nil
}

// storeMarker is the idempotency middleware's capturing writer: a RETRYABLE answer is marked so
// the middleware releases the key instead of storing the answer.
type storeMarker interface{ doNotStore() }

type keepMarker interface{ keepWrites() }

func writeError(w http.ResponseWriter, r *http.Request, err error) {
	p := problemFor(err)
	if p == internalProblem {
		slog.ErrorContext(r.Context(), "request failed", "error", logSafe(err.Error()), "method", logSafe(r.Method), "path", logSafe(r.URL.Path))
		writeProblem(w, r, p, "an unexpected error occurred")
		return
	}
	writeProblem(w, r, p, err.Error())
}

// writeProblem writes p; a RETRYABLE problem marks the capturing writer first.
func writeProblem(w http.ResponseWriter, r *http.Request, p problem, detail string) {
	if m, ok := w.(storeMarker); ok && p.class == classRetryable {
		m.doNotStore()
	}
	if m, ok := w.(keepMarker); ok && p.class == classOutcome {
		m.keepWrites()
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(p.status)
	_ = json.NewEncoder(w).Encode(problemDetail{Type: problemBaseURI + p.slug, Title: p.title, Status: p.status,
		Code: p.slug, Detail: detail, Instance: r.URL.EscapedPath()})
}

// recoverPanic turns a handler panic into the 500 problem; http.ErrAbortHandler keeps its meaning.
func recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			rec := recover()
			if rec == nil {
				return
			}
			if err, ok := rec.(error); ok && errors.Is(err, http.ErrAbortHandler) {
				panic(rec)
			}
			slog.ErrorContext(r.Context(), "handler panicked", "panic", logSafe(fmt.Sprint(rec)), "method", logSafe(r.Method), "path", logSafe(r.URL.Path))
			writeProblem(w, r, internalProblem, "an unexpected error occurred")
		}()
		next.ServeHTTP(w, r)
	})
}

// logSafe strips CR and LF from a request-derived value before it is logged.
func logSafe(s string) string { return strings.NewReplacer("\r", "", "\n", "").Replace(s) }
