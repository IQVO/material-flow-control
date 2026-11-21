package architecture_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// material-flow-control never speaks to equipment: MQTT stays inside asrs-control and
// amr-fleet-control (context map "MQTT stays inside the WCS"; relationships 6 and 7 only).
var forbiddenEquipmentImports = []string{"github.com/eclipse/paho.mqtt.golang"}

// TestNoPackageImportsAnEquipmentLink is the plan 04 Task 1 Step 8 test, split into helpers so it
// passes the repo's cyclop package average and gocognit limits; the skip rules and every assertion
// are byte-identical to the brief's single-function version.
func TestNoPackageImportsAnEquipmentLink(t *testing.T) {
	root := filepath.Join("..", "..")
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if skipEquipmentLinkDir(root, path, d) {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		return assertNoEquipmentImport(t, path)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func skipEquipmentLinkDir(root, path string, d os.DirEntry) bool {
	return d.IsDir() && (d.Name() == "vendor" || d.Name() == "node_modules" || strings.HasPrefix(d.Name(), ".")) && path != root
}

func assertNoEquipmentImport(t *testing.T, path string) error {
	t.Helper()
	f, perr := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
	if perr != nil {
		return perr
	}
	for _, imp := range f.Imports {
		for _, bad := range forbiddenEquipmentImports {
			if strings.Trim(imp.Path.Value, `"`) == bad {
				t.Errorf("%s imports %s: material-flow-control has no equipment link", path, bad)
			}
		}
	}
	return nil
}
