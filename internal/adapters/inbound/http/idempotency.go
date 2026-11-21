package http

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
	"unicode/utf8"

	"github.com/IQVO/material-flow-control/internal/application/idempotency"
	"github.com/IQVO/material-flow-control/internal/application/ports"
)

const (
	IdempotencyKeyHeader      = "Idempotency-Key"
	IdempotencyReplayedHeader = "Idempotency-Replayed"
	// IdempotencyWindow is how long an answer is replayed (fleet rule).
	IdempotencyWindow = 24 * time.Hour
)

var (
	errRollback = errors.New("the answer is a refusal: roll the request back")
	errLostRace = errors.New("another request stored an answer for this key first")
)

// Idempotency is the fleet Idempotency-Key middleware with replay classes. The handler runs in
// one unit of work (the use cases join it): a 2xx is stored in that transaction; a refusal rolls
// it back, and is then stored on its own only when FINAL; an OUTCOME refusal (a rejection, marked
// by writeRejection) keeps its writes: it is stored in the SAME transaction, which commits; a
// RETRYABLE refusal (marked by writeProblem), a 5xx and a panic are never stored, so the key is
// released. This unit of work is the OUTERMOST one of a REST call, so the subsystem commands the
// use cases queue (subsystem_commands rows, sent later by the dispatcher) commit with the answer
// and never exist for a rolled-back request; only the dispatcher kick and the transport counter run
// as UoW.AfterCommit hooks. carry (Task 18 review): comment reworded.
type Idempotency struct {
	Store  ports.IdempotencyStore
	UoW    ports.UnitOfWork
	Clock  ports.Clock
	Logger *slog.Logger
}

func NewIdempotency(store ports.IdempotencyStore, uow ports.UnitOfWork, clock ports.Clock) *Idempotency {
	return &Idempotency{Store: store, UoW: uow, Clock: clock, Logger: slog.Default()}
}

func (m *Idempotency) notBefore() time.Time { return m.Clock.Now().UTC().Add(-IdempotencyWindow) }

func (m *Idempotency) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get(IdempotencyKeyHeader)
		if key == "" || utf8.RuneCountInString(key) > maxIDLen {
			writeProblem(w, r, idempotencyKeyRequired, "send an Idempotency-Key header of 1 to 128 characters, unique per logical request")
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes+1))
		_ = r.Body.Close()
		if err != nil || len(body) > maxBodyBytes {
			writeError(w, r, errMalformedRequest)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		fp := fingerprint(r, body)
		rec, found, err := m.Store.Lookup(r.Context(), key, m.notBefore())
		if err != nil {
			writeError(w, r, err)
			return
		}
		if found {
			replay(w, r, rec, fp)
			return
		}
		m.serveFresh(w, r, next, key, fp)
	})
}

func fingerprint(r *http.Request, body []byte) string {
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "%s\n%s\n", r.Method, r.URL.EscapedPath())
	_, _ = h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

type storedAnswer struct {
	Header http.Header `json:"header,omitempty"`
	Body   []byte      `json:"body"`
}

// capture records the handler's answer before anything reaches the client.
type capture struct {
	header    http.Header
	code      int
	body      bytes.Buffer
	notStored bool
	keep      bool
}

func (c *capture) Header() http.Header { return c.header }
func (c *capture) doNotStore()         { c.notStored = true }

// keepWrites marks an OUTCOME refusal: its writes commit with the stored answer.
func (c *capture) keepWrites() { c.keep = true }

func (c *capture) WriteHeader(code int) {
	if c.code == 0 {
		c.code = code
	}
}

func (c *capture) Write(b []byte) (int, error) {
	if c.code == 0 {
		c.code = http.StatusOK
	}
	return c.body.Write(b)
}

func (m *Idempotency) serveFresh(w http.ResponseWriter, r *http.Request, next http.Handler, key, fp string) {
	var out *capture
	var winner idempotency.IdempotencyRecord
	err := m.UoW.Do(r.Context(), func(ctx context.Context) error {
		out = &capture{header: http.Header{}}
		next.ServeHTTP(out, r.WithContext(ctx))
		if out.code == 0 {
			out.code = http.StatusOK
		}
		if out.code >= http.StatusBadRequest && !out.keep {
			return errRollback
		}
		rec, err := m.record(key, fp, out)
		if err != nil {
			return err
		}
		stored, err := m.saveAndReread(ctx, rec)
		if err != nil {
			return err
		}
		if !sameAnswer(stored, rec) {
			winner = stored
			return errLostRace
		}
		return nil
	})
	switch {
	case err == nil:
		flush(w, out)
	case errors.Is(err, errLostRace):
		replay(w, r, winner, fp)
	case errors.Is(err, errRollback):
		m.finishRefused(w, r, key, fp, out)
	default:
		writeError(w, r, err)
	}
}

func (m *Idempotency) record(key, fp string, out *capture) (idempotency.IdempotencyRecord, error) {
	body, err := json.Marshal(storedAnswer{Header: out.header, Body: out.body.Bytes()})
	if err != nil {
		return idempotency.IdempotencyRecord{}, fmt.Errorf("encode idempotency answer: %w", err)
	}
	return idempotency.IdempotencyRecord{Key: key, Fingerprint: fp, Status: out.code, ContentType: out.header.Get("Content-Type"),
		Body: body, CreatedAt: m.Clock.Now().UTC().Truncate(time.Microsecond)}, nil
}

func (m *Idempotency) saveAndReread(ctx context.Context, rec idempotency.IdempotencyRecord) (idempotency.IdempotencyRecord, error) {
	if err := m.Store.Save(ctx, rec, m.notBefore()); err != nil {
		return idempotency.IdempotencyRecord{}, err
	}
	stored, found, err := m.Store.Lookup(ctx, rec.Key, m.notBefore())
	if err != nil {
		return idempotency.IdempotencyRecord{}, err
	}
	if !found {
		return idempotency.IdempotencyRecord{}, errors.New("idempotency answer vanished after it was saved")
	}
	return stored, nil
}

func sameAnswer(a, b idempotency.IdempotencyRecord) bool {
	return a.Fingerprint == b.Fingerprint && a.Status == b.Status && bytes.Equal(a.Body, b.Body) && a.CreatedAt.Equal(b.CreatedAt)
}

// finishRefused answers a rolled-back request: RETRYABLE and 5xx go out unstored (the key is
// released); a FINAL refusal is stored on its own so the same key and body replay it.
func (m *Idempotency) finishRefused(w http.ResponseWriter, r *http.Request, key, fp string, out *capture) {
	if out.code >= http.StatusInternalServerError || out.notStored {
		flush(w, out)
		return
	}
	rec, err := m.record(key, fp, out)
	if err == nil {
		err = m.Store.Save(r.Context(), rec, m.notBefore())
	}
	if err != nil {
		m.Logger.WarnContext(r.Context(), "idempotency: refusal not stored", "error", err)
		flush(w, out)
		return
	}
	if stored, found, err := m.Store.Lookup(r.Context(), key, m.notBefore()); err == nil && found && !sameAnswer(stored, rec) {
		replay(w, r, stored, fp)
		return
	}
	flush(w, out)
}

func replay(w http.ResponseWriter, r *http.Request, rec idempotency.IdempotencyRecord, fp string) {
	if rec.Fingerprint != fp {
		writeProblem(w, r, idempotencyKeyReused, "the Idempotency-Key was used with a different request (method, path or body)")
		return
	}
	var answer storedAnswer
	if err := json.Unmarshal(rec.Body, &answer); err != nil {
		writeError(w, r, fmt.Errorf("decode stored idempotency answer: %w", err))
		return
	}
	for k, vs := range answer.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.Header().Set(IdempotencyReplayedHeader, "true")
	w.WriteHeader(rec.Status)
	_, _ = w.Write(answer.Body)
}

func flush(w http.ResponseWriter, out *capture) {
	for k, vs := range out.header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(out.code)
	_, _ = w.Write(out.body.Bytes())
}
