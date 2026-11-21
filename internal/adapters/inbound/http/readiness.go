package http

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Dependency is one thing /readyz needs: Postgres, Kafka, every MQTT link.
type Dependency struct {
	Name  string
	Check func(ctx context.Context) error
}

// Readiness is the /readyz gate: a drain flag (SetNotReady, flipped first on shutdown) and
// dependency checks cached for ttl so probes cannot hammer them.
type Readiness struct {
	notReady  atomic.Bool
	deps      []Dependency
	ttl       time.Duration
	timeout   time.Duration
	now       func() time.Time
	mu        sync.Mutex
	checkedAt time.Time
	failing   []string
}

func NewReadiness(ttl, timeout time.Duration, deps ...Dependency) *Readiness {
	if ttl <= 0 {
		ttl = 3 * time.Second
	}
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	return &Readiness{deps: deps, ttl: ttl, timeout: timeout, now: time.Now}
}

func (g *Readiness) SetNotReady() {
	if g != nil {
		g.notReady.Store(true)
	}
}

func (g *Readiness) ready() bool { return g == nil || !g.notReady.Load() }

func (g *Readiness) failingDeps(ctx context.Context) []string {
	if g == nil || len(g.deps) == 0 {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.checkedAt.IsZero() && g.now().Sub(g.checkedAt) < g.ttl {
		return append([]string(nil), g.failing...)
	}
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), g.timeout)
	defer cancel()
	var failing []string
	for _, d := range g.deps {
		if err := d.Check(cctx); err != nil {
			slog.WarnContext(ctx, "readiness: dependency is not ready", "dependency", d.Name, "error", err)
			failing = append(failing, d.Name)
		}
	}
	g.failing, g.checkedAt = failing, g.now()
	return append([]string(nil), failing...)
}

func handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, statusBody{Status: "ok"})
}

func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if !s.Readiness.ready() {
		writeJSON(w, http.StatusServiceUnavailable, statusBody{Status: "not_ready"})
		return
	}
	if failing := s.Readiness.failingDeps(r.Context()); len(failing) > 0 {
		writeProblem(w, r, dependencyUnavailable, "not ready: "+strings.Join(failing, ", "))
		return
	}
	writeJSON(w, http.StatusOK, statusBody{Status: "ready"})
}
