//go:build integration

package postgres_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/IQVO/material-flow-control/internal/adapters/outbound/postgres"
	"github.com/IQVO/material-flow-control/internal/testing/pgtest"
)

// carry (retention review): the retention purges at scale (ADR 0008). The review's seeded postgres:16 (300k orders)
// found every order batch seq-scanning transport_legs once per purged order (the FK cascade had no index), 13.3 s a
// batch against the 5 s statement timeout, every hour, under the flow lock. These tests seed a bounded scale and
// read the plans Postgres actually ran, the cascade's included.

// purgedTables are the tables a purge deletes from: the retention policies' (ADR 0008) and cmd/api's
// idempotency and inbox purges.
var purgedTables = []string{"transport_orders", "container_moves", "subsystem_commands", "outbox_events",
	"idempotency_keys", "processed_events"}

// carry (retention review): a foreign key whose referenced row a purge deletes makes Postgres look up the referencing
// rows once per deleted row (ON DELETE CASCADE deletes them, NO ACTION checks them). Without an index led by the
// key's columns every lookup is a sequential scan of the referencing table.
func TestEveryForeignKeyIntoAPurgedTableHasAnIndexLedByItsColumns(t *testing.T) {
	pool := pgtest.Pool(t)
	rows, err := pool.Query(context.Background(), `SELECT c.conrelid::regclass::text || '.' || c.conname,
		EXISTS (SELECT 1 FROM pg_index i WHERE i.indrelid = c.conrelid AND i.indisvalid
			AND (string_to_array(i.indkey::text, ' ')::int2[])[1:cardinality(c.conkey)] = c.conkey)
		FROM pg_constraint c WHERE c.contype = 'f' AND (c.confdeltype = 'c' OR c.confrelid::regclass::text = ANY($1))`,
		purgedTables)
	if err != nil {
		t.Fatal(err)
	}
	checked, missing := []string{}, []string{}
	for rows.Next() {
		var name string
		var indexed bool
		if err := rows.Scan(&name, &indexed); err != nil {
			t.Fatal(err)
		}
		checked = append(checked, name)
		if !indexed {
			missing = append(missing, name)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(checked) == 0 {
		t.Fatal("no foreign key into a purged table was found: the sensor checks nothing (transport_legs has one)")
	}
	if len(missing) > 0 {
		t.Fatalf("foreign keys with no index led by their columns: %v (checked %v)", missing, checked)
	}
}

// plans collects the auto_explain plans (JSON) of every statement a pool's connections run, nested ones included:
// the RI trigger queries of a foreign key run as nested statements.
type plans struct {
	mu  sync.Mutex
	raw []string
}

func (p *plans) notice(_ *pgconn.PgConn, n *pgconn.Notice) {
	if i := strings.Index(n.Message, "{"); i >= 0 && strings.Contains(n.Message, "plan:") {
		p.mu.Lock()
		p.raw = append(p.raw, n.Message[i:])
		p.mu.Unlock()
	}
}

// explainedPool is a pool on url whose sessions log the plan of every statement, nested ones included, to the
// client (auto_explain at NOTICE), where p collects them; settings are extra session SETs.
func explainedPool(t *testing.T, url string, p *plans, settings ...string) *pgxpool.Pool {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
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
	return pool
}

type planNode struct {
	NodeType  string     `json:"Node Type"`
	Relation  string     `json:"Relation Name"`
	Index     string     `json:"Index Name"`
	IndexCond string     `json:"Index Cond"`
	Plans     []planNode `json:"Plans"`
}

// scansOf returns the node types that read relation in every collected plan.
func (p *plans) scansOf(t *testing.T, relation string) []string {
	t.Helper()
	var out []string
	p.walk(t, func(n planNode) {
		if n.Relation == relation {
			out = append(out, n.NodeType)
		}
	})
	return out
}

// indexConds returns the conditions every collected plan looks rows up by index with (a full index scan has none).
func (p *plans) indexConds(t *testing.T, index string) []string {
	t.Helper()
	var out []string
	p.walk(t, func(n planNode) {
		if n.Index == index && n.IndexCond != "" {
			out = append(out, n.IndexCond)
		}
	})
	return out
}

// walk calls visit on every node of every collected plan.
func (p *plans) walk(t *testing.T, visit func(planNode)) {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	var walk func(n planNode)
	walk = func(n planNode) {
		visit(n)
		for _, c := range n.Plans {
			walk(c)
		}
	}
	for _, raw := range p.raw {
		var doc struct {
			Plan planNode `json:"Plan"`
		}
		if err := json.Unmarshal([]byte(raw), &doc); err != nil {
			t.Fatalf("auto_explain plan is not JSON: %v: %.200s", err, raw)
		}
		walk(doc.Plan)
	}
}

// seedOrders writes n ended orders with one leg each; the first old of them were last written before the cutoff.
func seedOrders(t *testing.T, pool *pgxpool.Pool, n, old int) {
	t.Helper()
	exec(t, pool, `INSERT INTO transport_orders (transport_order_id, transport_request_id, container_id, status, internal,
		snapshot, version, accepted_at, updated_at)
		SELECT 'O-' || i, 'R-' || i, 'C-' || i, 'COMPLETED', false, '{}', 1, $3::timestamptz,
		CASE WHEN i <= $2 THEN $3::timestamptz ELSE $4::timestamptz END
		FROM generate_series(1, $1) i`, n, old, retOld, retRecent)
	exec(t, pool, `INSERT INTO transport_legs (leg_ref, transport_order_id) SELECT 'O-' || i || '.1.1', 'O-' || i
		FROM generate_series(1, $1) i`, n)
	exec(t, pool, `ANALYZE`)
}

// carry (retention review): 20k orders, 2k of them purgeable, in batches of 1000: no plan of the purge (the
// statement or the FK cascade it fires per order) reads transport_legs by a sequential scan, and the pass ends well
// inside the 5 s statement timeout every batch runs under.
func TestTheOrderPurgeNeverScansTheLegsTableAtScale(t *testing.T) {
	url := pgtest.URL(t)
	seed, err := postgres.NewPool(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(seed.Close)
	seedOrders(t, seed, 20000, 2000)
	var p plans
	start := time.Now()
	n, err := postgres.NewOrderRetention(explainedPool(t, url, &p), 1000).Purge(context.Background(), retCutoff)
	took := time.Since(start)
	if err != nil || n != 2000 {
		t.Fatalf("purge = %d, %v; want 2000 orders, no error (took %s)", n, err, took)
	}
	if took > 5*time.Second {
		t.Fatalf("the order purge took %s, want well inside the 5 s statement timeout", took)
	}
	scans := p.scansOf(t, "transport_legs")
	if len(scans) == 0 {
		t.Fatalf("no plan read transport_legs (%d plans collected): auto_explain did not capture the cascade", len(p.raw))
	}
	for _, s := range scans {
		if s == "Seq Scan" {
			t.Fatalf("a plan of the order purge seq-scans transport_legs: %d reads of it, by %v", len(scans), uniq(scans))
		}
	}
}

func uniq(s []string) []string {
	seen, out := map[string]bool{}, []string{}
	for _, v := range s {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}
