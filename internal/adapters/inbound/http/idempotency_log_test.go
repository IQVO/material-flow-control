package http

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/IQVO/material-flow-control/internal/application/idempotency"
	"github.com/IQVO/material-flow-control/internal/application/ports"
)

// lhFailingIdemStore fails every read and write; the error text carries a line break.
type lhFailingIdemStore struct{ ports.IdempotencyStore }

func (lhFailingIdemStore) Lookup(context.Context, string, time.Time) (idempotency.IdempotencyRecord, bool, error) {
	return idempotency.IdempotencyRecord{}, false, errors.New(lhForged)
}

func (lhFailingIdemStore) Save(context.Context, idempotency.IdempotencyRecord, time.Time) error {
	return errors.New(lhForged)
}

type lhClock struct{}

func (lhClock) Now() time.Time { return time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC) }

// A failed idempotency store call can carry request text (the key, the stored
// body); the warning must keep it on one line.
func TestLogHygiene_IdempotencyStoreFailureIsLoggedOnOneLine(t *testing.T) {
	cases := map[string]*capture{
		"answer not stored": {header: http.Header{}, code: http.StatusUnprocessableEntity},
		"re-read failed":    {header: http.Header{}, code: http.StatusInternalServerError},
	}
	for name, out := range cases {
		t.Run(name, func(t *testing.T) {
			values := lhRecordLogs(t)
			m := &Idempotency{Store: lhFailingIdemStore{}, Clock: lhClock{}, Logger: slog.Default()}
			r := httptest.NewRequest(http.MethodPost, "/resources", nil)
			m.finishRefused(httptest.NewRecorder(), r, "key-1", "fp-1", out)
			lhAssertOneLine(t, *values, name)
		})
	}
}
