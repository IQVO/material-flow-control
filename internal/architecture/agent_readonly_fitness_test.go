package architecture

// Agent surface fitness (ADR 0007): the MCP server and the A2A agent are READ-ONLY adapters.
//
//   - TestA2AAdapterDependencyRule: the A2A adapter depends only on the application and domain
//     layers, and nothing in internal/ depends on it (the MCP twin is TestMCPAdapterDependencyRule).
//   - TestAgentSurfacesAreReadOnly: an ALLOWLIST sensor over each adapter and its cmd/ composition
//     root. The adapter's `Reads` seam holds exactly the listed read methods; the only
//     usecases/ports/postgres identifiers the sources may reference are the read ones; and no
//     write-side adapter (Kafka, the Kafka consumers, outbox, events, the asrs-control and
//     amr-fleet-control clients, the memory doubles), no sibling inbound adapter and no outbound A2A
//     client is imported. Allowlist, not denylist: a new write use case is refused without having to
//     be named here first. A surface whose adapter AND cmd/ directories are both absent is not built
//     yet (cmd/a2a lands in Task 21) and is reported, not checked; one without the other fails.
//   - TestAgentReadOnlySensorBites proves each rule fires on code fixtures, so the sensor cannot
//     pass vacuously.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	archgo "github.com/arch-go/arch-go/api"
	"github.com/arch-go/arch-go/api/configuration"
)

func TestA2AAdapterDependencyRule(t *testing.T) {
	moduleInfo := configuration.Load(modulePath)
	t.Run("a2a adapter depends only on application and domain", func(t *testing.T) {
		assertArchGoPasses(t, archgo.CheckArchitecture(moduleInfo, configuration.Config{DependenciesRules: []*configuration.DependenciesRule{{
			Package: "**.internal.adapters.inbound.a2a.**",
			ShouldOnlyDependsOn: &configuration.Dependencies{Internal: []string{"**.internal.adapters.inbound.a2a.**",
				"**.internal.application.**", "**.internal.domain.**"}},
		}}}))
	})
	t.Run("nothing else depends on the a2a adapter", func(t *testing.T) {
		assertArchGoPasses(t, archgo.CheckArchitecture(moduleInfo, configuration.Config{DependenciesRules: []*configuration.DependenciesRule{{
			Package:            "**.internal.**",
			ShouldNotDependsOn: &configuration.Dependencies{Internal: []string{"**.internal.adapters.inbound.a2a.**"}},
		}}}))
	})
}

// agentSurface is one read-only agent adapter and its composition root.
type agentSurface struct {
	name, adapterDir, cmdDir string
	reads                    []string // the exact method set of the adapter's Reads seam, sorted
	forbidSibling            string   // the other agent adapter, never imported
}

// agentReads is the read method set of both Reads seams (*usecases.Reader implements it).
var agentReads = []string{"EquipmentUnits", "OrderDetail", "Station", "Subsystems", "Track", "Transport"}

var agentSurfaces = []agentSurface{
	{"mcp", "../adapters/inbound/mcp", "../../cmd/mcp", agentReads, "/internal/adapters/inbound/a2a"},
	{"a2a", "../adapters/inbound/a2a", "../../cmd/a2a", agentReads, "/internal/adapters/inbound/mcp"},
}

// agentAllowedSelectors maps a package qualifier to the identifiers agent sources may reference
// through it. Anything else (a write use case, the unit of work, the outbox) is a violation.
var agentAllowedSelectors = map[string][]string{
	"ports": {},
	"usecases": {"NewReader", "ReadDeps", "Reader", "DefaultConfig", "Config", "TransportView", "TrackView",
		"ErrRequestNotFound", "ErrTrackNotFound", "ErrStationNotFound", "ErrInvalidRequest"},
	"postgres": {"MigrationsURL", "RunMigrations", "NewPool", "NewRequests", "NewOrders", "NewTracks", "NewStations",
		"NewCapacities", "NewUnits"},
}

// agentForbiddenImports are import-path fragments no agent source may use, and why.
var agentForbiddenImports = map[string]string{
	"a2a-go/v2/a2aclient":                       "an outbound A2A client (the agent only answers)",
	"github.com/segmentio/kafka-go":             "Kafka: a read-only surface never dials Kafka",
	"/internal/adapters/outbound/kafka":         "Kafka: a read-only surface publishes no fact",
	"/internal/adapters/inbound/kafka":          "the subsystem fact consumers: only cmd/api consumes",
	"/internal/adapters/outbound/outbox":        "the outbox relay: a read-only surface never publishes",
	"/internal/adapters/outbound/events":        "the event sink: a read-only surface never appends an event",
	"/internal/adapters/outbound/asrs":          "the asrs-control client: only the cmd/api dispatcher calls a subsystem",
	"/internal/adapters/outbound/amr":           "the amr-fleet-control client: only the cmd/api dispatcher calls a subsystem",
	"/internal/adapters/outbound/subsystemhttp": "the subsystem REST transport: only the cmd/api dispatcher calls a subsystem",
	"/internal/adapters/outbound/memory":        "the in-memory test doubles (real Postgres only at runtime)",
	"/internal/adapters/inbound/http":           "the REST adapter (the inbound adapters are siblings)",
}

func agentSourceViolations(s agentSurface, filename string, src any) ([]string, error) {
	f, err := parser.ParseFile(token.NewFileSet(), filename, src, 0)
	if err != nil {
		return nil, err
	}
	out := agentImportViolations(s, f)
	out = append(out, agentSelectorViolations(f)...)
	return append(out, agentReadsViolations(s, f)...), nil
}

func agentImportViolations(s agentSurface, f *ast.File) []string {
	var out []string
	for _, imp := range f.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		for fragment, why := range agentForbiddenImports {
			if strings.Contains(path, fragment) {
				out = append(out, "imports "+path+": "+why)
			}
		}
		if strings.Contains(path, s.forbidSibling) {
			out = append(out, "imports "+path+": the agent adapters are siblings")
		}
	}
	return out
}

func agentSelectorViolations(f *ast.File) []string {
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		if allowed, guarded := agentAllowedSelectors[pkg.Name]; guarded && !slices.Contains(allowed, sel.Sel.Name) {
			out = append(out, "references "+pkg.Name+"."+sel.Sel.Name+", which is not on the read-only allowlist")
		}
		return true
	})
	return out
}

func agentReadsViolations(s agentSurface, f *ast.File) []string {
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		spec, ok := n.(*ast.TypeSpec)
		if !ok || spec.Name.Name != "Reads" {
			return true
		}
		iface, ok := spec.Type.(*ast.InterfaceType)
		if !ok {
			return true
		}
		var got []string
		for _, m := range iface.Methods.List {
			for _, name := range m.Names {
				got = append(got, name.Name)
			}
		}
		sort.Strings(got)
		if strings.Join(got, ",") != strings.Join(s.reads, ",") {
			out = append(out, "Reads declares "+strings.Join(got, ",")+", want exactly "+strings.Join(s.reads, ","))
		}
		return false
	})
	return out
}

func TestAgentSurfacesAreReadOnly(t *testing.T) {
	for _, s := range agentSurfaces {
		if !dirExists(s.adapterDir) && !dirExists(s.cmdDir) && s.name != "mcp" {
			t.Logf("the %s surface is not built yet (neither %s nor %s exists): nothing to check", s.name, s.adapterDir, s.cmdDir)
			continue
		}
		for _, dir := range []string{s.adapterDir, s.cmdDir} {
			files := goFilesUnder(t, dir, false)
			if len(files) == 0 {
				t.Fatalf("no %s sources under %s; the sensor is looking in the wrong place", s.name, dir)
			}
			for _, path := range files {
				for _, v := range agentFileViolations(t, s, path) {
					t.Errorf("%s %s (ADR 0007: the %s surface is read-only)", path, v, s.name)
				}
			}
		}
	}
}

func dirExists(dir string) bool {
	info, err := os.Stat(dir)
	return err == nil && info.IsDir()
}

func agentFileViolations(t *testing.T, s agentSurface, path string) []string {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	violations, err := agentSourceViolations(s, path, src)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return violations
}

func TestAgentReadOnlySensorBites(t *testing.T) {
	a2aSurface, mcpSurface := agentSurfaces[1], agentSurfaces[0]
	bad := map[string]string{
		"a write use case":        "package x\nvar _ = usecases.NewService\n",
		"the unit of work":        "package x\nvar _ = postgres.NewUnitOfWork\n",
		"the flow lock":           "package x\nvar _ = postgres.NewFlowLock\n",
		"the outbox repo":         "package x\nvar _ = postgres.NewOutboxRepo\n",
		"the command outbox":      "package x\nvar _ = postgres.NewCommands\n",
		"a port":                  "package x\nvar _ = ports.UnitOfWork\n",
		"a Kafka import":          "package x\nimport _ \"github.com/IQVO/material-flow-control/internal/adapters/outbound/kafka\"\n",
		"kafka-go":                "package x\nimport _ \"github.com/segmentio/kafka-go\"\n",
		"the fact consumers":      "package x\nimport _ \"github.com/IQVO/material-flow-control/internal/adapters/inbound/kafka\"\n",
		"the outbox relay":        "package x\nimport _ \"github.com/IQVO/material-flow-control/internal/adapters/outbound/outbox\"\n",
		"the event sink":          "package x\nimport _ \"github.com/IQVO/material-flow-control/internal/adapters/outbound/events\"\n",
		"the asrs-control client": "package x\nimport _ \"github.com/IQVO/material-flow-control/internal/adapters/outbound/asrs\"\n",
		"the amr client":          "package x\nimport _ \"github.com/IQVO/material-flow-control/internal/adapters/outbound/amr\"\n",
		"the subsystem transport": "package x\nimport _ \"github.com/IQVO/material-flow-control/internal/adapters/outbound/subsystemhttp\"\n",
		"an A2A client":           "package x\nimport _ \"github.com/a2aproject/a2a-go/v2/a2aclient\"\n",
		"a memory double":         "package x\nimport _ \"github.com/IQVO/material-flow-control/internal/adapters/outbound/memory\"\n",
		"the REST adapter":        "package x\nimport _ \"github.com/IQVO/material-flow-control/internal/adapters/inbound/http\"\n",
		"the sibling MCP adapter": "package x\nimport _ \"github.com/IQVO/material-flow-control/internal/adapters/inbound/mcp\"\n",
		"a write method on Reads": "package x\ntype Reads interface{ EquipmentUnits(); OrderDetail(); Station(); Subsystems(); Track(); Transport(); Cancel() }\n",
		"a missing read method":   "package x\ntype Reads interface{ Transport() }\n",
	}
	for name, src := range bad {
		if got, err := agentSourceViolations(a2aSurface, "fixture.go", src); err != nil || len(got) == 0 {
			t.Errorf("the sensor does not flag %s (violations=%v, err=%v)", name, got, err)
		}
	}
	good := "package x\n// usecases.NewService and postgres.NewUnitOfWork are only named in this comment\n" +
		"import _ \"github.com/a2aproject/a2a-go/v2/a2asrv\"\n" +
		"var _ = usecases.NewReader\nvar _ = postgres.NewOrders\n" +
		"type Reads interface{ Transport(); OrderDetail(); Track(); Station(); Subsystems(); EquipmentUnits() }\n"
	if got, err := agentSourceViolations(a2aSurface, "fixture.go", good); err != nil || len(got) != 0 {
		t.Errorf("false positive on the read wiring: %v (err=%v)", got, err)
	}
	if got, _ := agentSourceViolations(mcpSurface, "fixture.go", "package x\nimport _ \"github.com/IQVO/material-flow-control/internal/adapters/inbound/a2a\"\n"); len(got) == 0 {
		t.Error("the sensor does not flag the MCP sources importing the A2A adapter")
	}
}
