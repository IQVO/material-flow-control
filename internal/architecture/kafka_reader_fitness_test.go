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

// readerConfigs finds every kafkago.ReaderConfig composite literal in production code under internal/ and cmd/.
func readerConfigs(t *testing.T) map[string]*ast.CompositeLit {
	t.Helper()
	out := map[string]*ast.CompositeLit{}
	for _, root := range []string{"../../internal", "../../cmd"} {
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			fset := token.NewFileSet()
			f, perr := parser.ParseFile(fset, path, nil, 0)
			if perr != nil {
				t.Fatal(perr)
			}
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

func TestKafkaConsumerGroupsComeFromTheCallerAndPerProcessGroupsSetCommitInterval(t *testing.T) {
	lits := readerConfigs(t)
	if len(lits) == 0 {
		t.Fatal("no kafkago.ReaderConfig found: the walk is broken")
	}
	for pos, lit := range lits {
		group := field(lit, "GroupID")
		if _, literal := group.(*ast.BasicLit); literal {
			t.Errorf("%s: a literal GroupID (groups come from MFC_*_CONSUMER_GROUP)", pos)
		}
		if _, computed := group.(*ast.CallExpr); computed && field(lit, "CommitInterval") == nil {
			t.Errorf("%s: a per-process (computed) GroupID without CommitInterval: kafka-go then commits synchronously per message", pos)
		}
		if field(lit, "MaxWait") != nil {
			t.Errorf("%s: MaxWait set (checklist item 12: leave it unset)", pos)
		}
	}
}
