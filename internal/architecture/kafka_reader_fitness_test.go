package architecture_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// readerConfigLits collects every kafkago.ReaderConfig composite literal of one parsed file, keyed by position.
func readerConfigLits(fset *token.FileSet, f *ast.File, out map[string]*ast.CompositeLit) {
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		if sel, ok := lit.Type.(*ast.SelectorExpr); ok && sel.Sel.Name == "ReaderConfig" {
			out[fset.Position(lit.Pos()).String()] = lit
		}
		return true
	})
}

// readerConfigs finds every kafkago.ReaderConfig composite literal in production code under internal/ and cmd/.
// internal/testing is test support (integration-only helpers such as kafkatest's partition reader that reads a
// topic back), never a consumer, so it is not walked.
func readerConfigs(t *testing.T) map[string]*ast.CompositeLit {
	t.Helper()
	out := map[string]*ast.CompositeLit{}
	for _, root := range []string{"../../internal", "../../cmd"} {
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err == nil && d.IsDir() && filepath.ToSlash(path) == "../../internal/testing" {
				return filepath.SkipDir
			}
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			fset := token.NewFileSet()
			f, perr := parser.ParseFile(fset, path, nil, 0)
			if perr != nil {
				t.Fatal(perr)
			}
			readerConfigLits(fset, f, out)
			return nil
		})
	}
	return out
}

func field(lit *ast.CompositeLit, name string) ast.Expr {
	for _, e := range lit.Elts {
		if kv, ok := e.(*ast.KeyValueExpr); ok {
			if id, ok := kv.Key.(*ast.Ident); ok && id.Name == name {
				return kv.Value
			}
		}
	}
	return nil
}

// readerConfigViolations is the rule for one ReaderConfig literal.
func readerConfigViolations(pos string, lit *ast.CompositeLit) []string {
	var out []string
	group := field(lit, "GroupID")
	if group == nil {
		// carry (Task 15 review): a reader without a group commits nothing, so a restart replays or skips.
		out = append(out, pos+": no GroupID (both consumers are fixed-group at-least-once readers)")
	}
	if _, literal := group.(*ast.BasicLit); literal {
		out = append(out, pos+": a literal GroupID (groups come from MFC_*_CONSUMER_GROUP)")
	}
	if _, computed := group.(*ast.CallExpr); computed && field(lit, "CommitInterval") == nil {
		out = append(out, pos+": a per-process (computed) GroupID without CommitInterval: kafka-go then commits synchronously per message")
	}
	if field(lit, "MaxWait") != nil {
		out = append(out, pos+": MaxWait set (checklist item 12: leave it unset)")
	}
	return out
}

func TestKafkaConsumerGroupsComeFromTheCallerAndPerProcessGroupsSetCommitInterval(t *testing.T) {
	lits := readerConfigs(t)
	if len(lits) == 0 {
		t.Fatal("no kafkago.ReaderConfig found: the walk is broken")
	}
	for pos, lit := range lits {
		for _, v := range readerConfigViolations(pos, lit) {
			t.Error(v)
		}
	}
}

// carry (Task 15 review): the detector bites on fixture sources, one subtest per rule, and passes the good shapes.
func TestTheKafkaReaderSensorFlagsEveryBadReaderConfig(t *testing.T) {
	cases := []struct {
		name, body string
		flagged    bool
	}{
		{"literal GroupID", `kafkago.ReaderConfig{Brokers: b, Topic: t, GroupID: "material-flow-control.asrs-control-events"}`, true},
		{"Sprintf GroupID without CommitInterval", `kafkago.ReaderConfig{Brokers: b, Topic: t, GroupID: fmt.Sprintf("mfc-%s", host)}`, true},
		{"MaxWait set", `kafkago.ReaderConfig{Brokers: b, Topic: t, GroupID: groupID, MaxWait: 500 * time.Millisecond}`, true},
		{"no GroupID", `kafkago.ReaderConfig{Brokers: b, Topic: t, StartOffset: kafkago.FirstOffset}`, true},
		{"GroupID from the caller", `kafkago.ReaderConfig{Brokers: b, Topic: t, GroupID: groupID, StartOffset: kafkago.FirstOffset}`, false},
		{"Sprintf GroupID with CommitInterval", `kafkago.ReaderConfig{Brokers: b, Topic: t, GroupID: fmt.Sprintf("mfc-%s", host), CommitInterval: time.Second}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := "package fixture\n\nfunc reader() any { return " + tc.body + " }\n"
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, "fixture.go", src, 0)
			if err != nil {
				t.Fatal(err)
			}
			lits := map[string]*ast.CompositeLit{}
			readerConfigLits(fset, f, lits)
			if len(lits) != 1 {
				t.Fatalf("found %d ReaderConfig literals, want 1", len(lits))
			}
			for pos, lit := range lits {
				if got := readerConfigViolations(pos, lit); (len(got) > 0) != tc.flagged {
					t.Fatalf("violations %v, want flagged = %v", got, tc.flagged)
				}
			}
		})
	}
}
