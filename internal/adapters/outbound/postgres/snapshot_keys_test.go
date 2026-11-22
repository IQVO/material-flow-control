package postgres

import (
	"flag"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/IQVO/material-flow-control/internal/application/facts"
	"github.com/IQVO/material-flow-control/internal/domain/capacity"
	"github.com/IQVO/material-flow-control/internal/domain/containertrack"
	"github.com/IQVO/material-flow-control/internal/domain/stationqueue"
	"github.com/IQVO/material-flow-control/internal/domain/transportorder"
	"github.com/IQVO/material-flow-control/internal/domain/transportrequest"
)

// carry (Task 13 review): the JSONB snapshot evolution rule (ADR 0002 decision 6). Every value this adapter stores
// as JSON is encoded from its Go type (exported field names, no tags in the domain), so renaming or retyping a field
// silently changes the stored shape. This golden pins the sorted key paths of every stored type: an ADDED path is
// fine (regenerate with -update-snapshot-keys); a renamed, retyped or removed one needs a numbered migration that
// rewrites the JSONB columns in the same change.
var updateSnapshotKeys = flag.Bool("update-snapshot-keys", false, "rewrite testdata/snapshot_keys.golden")

const snapshotKeysGolden = "testdata/snapshot_keys.golden"

var storedJSON = []struct {
	column string
	value  any
}{
	{"transport_requests.record", transportrequest.Record{}},
	{"transport_orders.snapshot", transportorder.Snapshot{}},
	{"container_tracks.snapshot", containertrack.Snapshot{}},
	{"container_moves.move", containertrack.Move{}},
	{"station_queues.snapshot", stationqueue.Snapshot{}},
	{"subsystem_capacities.snapshot", capacity.Snapshot{}},
	{"equipment_units.state", facts.EquipmentState{}},
}

func TestTheStoredJSONShapesArePinned(t *testing.T) {
	var b strings.Builder
	for _, s := range storedJSON {
		paths := keyPaths(reflect.TypeOf(s.value), s.column, map[reflect.Type]bool{})
		sort.Strings(paths)
		for _, p := range paths {
			b.WriteString(p + "\n")
		}
	}
	got := b.String()
	if *updateSnapshotKeys {
		if err := os.WriteFile(snapshotKeysGolden, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(snapshotKeysGolden)
	if err != nil {
		t.Fatalf("read %s: %v (generate it with -update-snapshot-keys)", snapshotKeysGolden, err)
	}
	if got != string(want) {
		t.Fatalf("the stored JSON shapes changed (ADR 0002 decision 6): an added path is fine, regenerate with "+
			"`go test ./internal/adapters/outbound/postgres -run TestTheStoredJSONShapesArePinned -update-snapshot-keys`; "+
			"a renamed, retyped or removed path needs a numbered migration rewriting the JSONB columns.\n got:\n%s\nwant:\n%s", got, want)
	}
}

var timeType = reflect.TypeOf(time.Time{})

// keyPaths lists "<path> <leaf type>" for every JSON key path of t as encoding/json writes it.
func keyPaths(t reflect.Type, prefix string, seen map[reflect.Type]bool) []string {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch {
	case t == timeType:
		return []string{prefix + " time"}
	case t.Kind() == reflect.Struct:
		return structPaths(t, prefix, seen)
	case (t.Kind() == reflect.Slice || t.Kind() == reflect.Array) && t.Elem().Kind() != reflect.Uint8:
		return keyPaths(t.Elem(), prefix+"[]", seen)
	case t.Kind() == reflect.Map:
		return keyPaths(t.Elem(), prefix+"{}", seen)
	default:
		return []string{prefix + " " + t.Kind().String()}
	}
}

func structPaths(t reflect.Type, prefix string, seen map[reflect.Type]bool) []string {
	if seen[t] {
		return []string{prefix + " (recursive " + t.String() + ")"}
	}
	seen[t] = true
	defer delete(seen, t)
	var out []string
	for i := range t.NumField() {
		f := t.Field(i)
		name, ok := jsonName(f)
		if !ok {
			continue
		}
		if f.Anonymous && name == "" {
			out = append(out, keyPaths(f.Type, prefix, seen)...)
			continue
		}
		if name == "" {
			name = f.Name
		}
		out = append(out, keyPaths(f.Type, prefix+"."+name, seen)...)
	}
	return out
}

// jsonName is the key encoding/json uses for f ("" = the Go name, or flattened when anonymous); ok is false for a
// field encoding/json skips.
func jsonName(f reflect.StructField) (string, bool) {
	if !f.IsExported() && !f.Anonymous {
		return "", false
	}
	tag := f.Tag.Get("json")
	if tag == "-" {
		return "", false
	}
	name, _, _ := strings.Cut(tag, ",")
	return name, true
}
