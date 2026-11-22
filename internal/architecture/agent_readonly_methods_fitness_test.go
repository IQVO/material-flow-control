package architecture

import (
	"go/ast"
	"go/types"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// agentWriteVerbs are the method names agent sources may not select on ANY value (carry, Task 21 review): the
// write methods of the postgres repos (Save, Insert, Append, Enqueue, Lock, Purge, Delete, Update) and of the
// pgx pool and its transactions (Exec, Begin, BeginTx, CopyFrom, SendBatch). A verb list, not a type check: the
// sensor parses (fixtures need no module to compile against), so a selector is judged by its name.
//
// carry (plan 03 Task 18 review): the pool's raw-SQL entry points are refused too (Query, QueryRow, Acquire,
// AcquireFunc, AcquireAllIdle, Conn, PgConn, ExecParams, Hijack): a raw pool.Query runs ANY SQL in autocommit,
// OUTSIDE the BEGIN READ ONLY every adapter transaction of postgres.NewReadOnlyPool opens (ruling P4-R17), so it
// is a write seam. The only legit pool methods in agent sources are Close and Ping; the Reader's own reads go
// through the postgres adapter, never a raw pool call.
//
// False-positive allowances, by design: a package-qualified FUNCTION is not a method and is judged by the
// allowlist above instead (slices.Insert, slices.Delete, maps.DeleteFunc stay allowed; usecases/postgres
// identifiers keep their allowlist). Anything else with one of these names is refused even when it is not a
// database write (a sync.Mutex Lock, a strings.Builder-like Append): an agent source has no business with either,
// and a real need adds its exact expression to agentWriteVerbAllowances with the reason.
var agentWriteVerbs = []string{"Save", "Insert", "Exec", "Begin", "BeginTx", "CopyFrom", "SendBatch", "Enqueue",
	"Append", "Lock", "Purge", "Delete", "Update",
	"Query", "QueryRow", "Acquire", "AcquireFunc", "AcquireAllIdle", "Conn", "PgConn", "ExecParams", "Hijack"}

// agentWriteVerbAllowances maps an exact selector expression (types.ExprString) to why it is not a write. Empty:
// no agent source needs one today.
var agentWriteVerbAllowances = map[string]string{}

// agentRawDriverImports are refused outright: the read pool is built by postgres.NewReadOnlyPool only, so a raw driver
// import can only mean a second, unguarded way to the database.
var agentRawDriverImports = []string{"github.com/jackc/pgx", "database/sql"}

// agentWriteMethodViolations refuses a write verb selected on anything but an imported package, and a raw
// driver import. names resolves each local package name of f (agentQualifiers).
func agentWriteMethodViolations(f *ast.File, names map[string]string) []string {
	var out []string
	for _, imp := range f.Imports {
		if path, err := strconv.Unquote(imp.Path.Value); err == nil {
			for _, raw := range agentRawDriverImports {
				if path == raw || strings.HasPrefix(path, raw+"/") {
					out = append(out, "imports "+path+": a raw database driver (the read pool comes from postgres.NewReadOnlyPool only)")
				}
			}
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok || !slices.Contains(agentWriteVerbs, sel.Sel.Name) {
			return true
		}
		if pkg, isIdent := sel.X.(*ast.Ident); isIdent && pkg.Obj == nil && names[pkg.Name] != "" {
			return true // a package-qualified function: the allowlist judges it
		}
		expr := types.ExprString(sel)
		if _, allowed := agentWriteVerbAllowances[expr]; !allowed {
			out = append(out, "selects "+expr+": "+sel.Sel.Name+" is a write method or raw SQL (the surface reads only)")
		}
		return true
	})
	return out
}

// carry (Task 21 review): the allowlist above judges package-qualified identifiers only, so a write METHOD on a
// value an allowlisted constructor returned (postgres.NewTracks(pool).Save, pool.Exec) and a raw driver import
// got through. Each fixture below passed TestAgentSurfacesAreReadOnly's rules on 6944c95.
func TestAgentReadOnlySensorRefusesWriteMethodsAndRawDrivers(t *testing.T) {
	a2aSurface := agentSurfaces[1]
	pg := "import \"" + modulePath + "/internal/adapters/outbound/postgres\"\n"
	bad := map[string]string{
		"a write method on a constructed repo": "package x\n" + pg + "func f() { _ = postgres.NewTracks(nil).Save(ctx, nil) }\n",
		"a write method on a repo variable":    "package x\n" + pg + "func f() { r := postgres.NewRequests(nil); _ = r.Insert(ctx, tr.Record{}) }\n",
		"a write method on the units repo":     "package x\n" + pg + "func f() { _ = postgres.NewUnits(nil).Save(ctx, nil) }\n",
		"Exec on the pool":                     "package x\n" + pg + "func f() { p, _ := postgres.NewPool(ctx, dsn); _, _ = p.Exec(ctx, \"DELETE FROM transport_requests\") }\n",
		"a method value of a write":            "package x\n" + pg + "func f() { save := postgres.NewTracks(nil).Save; _ = save }\n",
		"a write method through a field":       "package x\nfunc (d deps) f() { _, _ = d.pool.Begin(ctx) }\n",
		"a method expression of a write":       "package x\n" + pg + "var _ = (*postgres.Tracks).Save\n",
		"a pgx import":                         "package x\nimport _ \"github.com/jackc/pgx/v5/pgxpool\"\n",
		"a pgx root import":                    "package x\nimport _ \"github.com/jackc/pgx/v5\"\n",
		"a database/sql import":                "package x\nimport _ \"database/sql\"\n",
		// carry (plan 03 Task 18 review): raw SQL through the read-only pool runs autocommit OUTSIDE BEGIN READ ONLY.
		"raw SQL through pool.Query":    "package x\n" + pg + "func f() { p, _ := postgres.NewReadOnlyPool(ctx, dsn); _, _ = p.Query(ctx, \"DELETE FROM transport_requests\") }\n",
		"raw SQL through pool.QueryRow": "package x\n" + pg + "func f() { p, _ := postgres.NewReadOnlyPool(ctx, dsn); _ = p.QueryRow(ctx, \"UPDATE transport_requests SET status = 'x' RETURNING 1\") }\n",
		"a pooled connection":           "package x\n" + pg + "func f() { p, _ := postgres.NewReadOnlyPool(ctx, dsn); c, _ := p.Acquire(ctx); _ = c.Conn().PgConn().ExecParams(ctx, \"DELETE FROM transport_requests\", nil, nil, nil, nil) }\n",
	}
	// carry (plan 03 Task 18 review): each pgx raw-SQL method, selected on any value.
	for _, m := range []string{"Query", "QueryRow", "Acquire", "AcquireFunc", "AcquireAllIdle", "Conn", "PgConn", "ExecParams", "Hijack"} {
		bad["the raw-SQL method "+m] = "package x\nfunc f(v pool) { _ = v." + m + " }\n"
	}
	for _, verb := range agentWriteVerbs {
		bad["the write verb "+verb] = "package x\nfunc f(v repo) { _ = v." + verb + " }\n"
	}
	for name, src := range bad {
		if got, err := agentSourceViolations(a2aSurface, "fixture.go", src); err != nil || len(got) == 0 {
			t.Errorf("the sensor does not flag %s (violations=%v, err=%v)", name, got, err)
		}
	}
	// The read wiring, package-qualified slice helpers and non-write methods stay allowed.
	good := "package x\nimport \"slices\"\n" + pg +
		"func f() { pool, _ := postgres.NewReadOnlyPool(ctx, dsn); defer pool.Close(); _ = pool.Ping(ctx)\n" +
		"r := usecases.NewReader(usecases.ReadDeps{Requests: postgres.NewRequests(pool), Units: postgres.NewUnits(pool)})\n" +
		"_, _ = r.Transport(ctx, id); _, _ = r.EquipmentUnits(ctx, id); s := slices.Insert(xs, 0, 1); _ = slices.Delete(s, 0, 1) }\n"
	if got, err := agentSourceViolations(a2aSurface, "fixture.go", good); err != nil || len(got) != 0 {
		t.Errorf("false positive on the read wiring: %v (err=%v)", got, err)
	}
}
