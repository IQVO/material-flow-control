//go:build integration

package pgtest

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Plans collects the auto_explain plans (JSON) of every statement an ExplainedPool's sessions run, nested ones
// included: the RI trigger queries a foreign key fires run as nested statements. carry (retention review)
type Plans struct {
	mu  sync.Mutex
	raw []string
}

// PlanNode is one node of a collected plan.
type PlanNode struct {
	NodeType  string     `json:"Node Type"`
	Relation  string     `json:"Relation Name"`
	Index     string     `json:"Index Name"`
	IndexCond string     `json:"Index Cond"`
	Plans     []PlanNode `json:"Plans"`
}

func (p *Plans) notice(_ *pgconn.PgConn, n *pgconn.Notice) {
	if i := strings.Index(n.Message, "{"); i >= 0 && strings.Contains(n.Message, "plan:") {
		p.mu.Lock()
		p.raw = append(p.raw, n.Message[i:])
		p.mu.Unlock()
	}
}

// Len is the number of plans collected.
func (p *Plans) Len() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.raw)
}

// Walk calls visit on every node of every collected plan.
func (p *Plans) Walk(t *testing.T, visit func(PlanNode)) {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	var walk func(n PlanNode)
	walk = func(n PlanNode) {
		visit(n)
		for _, c := range n.Plans {
			walk(c)
		}
	}
	for _, raw := range p.raw {
		var doc struct {
			Plan PlanNode `json:"Plan"`
		}
		if err := json.Unmarshal([]byte(raw), &doc); err != nil {
			t.Fatalf("auto_explain plan is not JSON: %v: %.200s", err, raw)
		}
		walk(doc.Plan)
	}
}

// ExplainedPool is a pool on url (a database of this package's container) whose sessions log the plan of every
// statement, nested ones included, to the client (auto_explain at NOTICE), where the returned Plans collect them;
// settings are extra session SETs. Closed at cleanup.
func ExplainedPool(t *testing.T, url string, settings ...string) (*pgxpool.Pool, *Plans) {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	p := &Plans{}
	cfg.ConnConfig.OnNotice = p.notice
	cfg.AfterConnect = func(ctx context.Context, c *pgx.Conn) error {
		_, err := c.Exec(ctx, `LOAD 'auto_explain'; SET auto_explain.log_min_duration = 0;
			SET auto_explain.log_nested_statements = on; SET auto_explain.log_format = json;
			SET auto_explain.log_level = notice`)
		for _, s := range settings {
			if err == nil {
				_, err = c.Exec(ctx, s)
			}
		}
		return err
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool, p
}
