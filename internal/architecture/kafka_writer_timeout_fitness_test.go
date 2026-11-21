package architecture

// Kafka writer timeout sensor (material-flow-control incident 2026-11-20, e2e's first CI runs).
//
//   - TestKafkaWritersSetTimeouts: every kafka-go Writer composite literal in a
//     non-test file under internal/ or cmd/ must set WriteTimeout and ReadTimeout.
//     With RequiredAcks=All and no deadlines, WriteMessages on a half-open broker
//     connection blocks forever: the outbox relay held its claimed-row locks the
//     whole time, its in-flight published marks never committed, later passes
//     SKIP LOCKED past the rows in silence (zero error logs), and every event
//     after the stall rotted unpublished. The e2e lost exactly the HELD
//     EquipmentStateChanged and its capacity fact this way while the unit row
//     did turn HELD.
//   - TestKafkaWriterTimeoutSensorBites proves the sensor flags a literal
//     missing the keys under any import alias and accepts one that sets them.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// writersWithoutTimeouts returns how many kafka-go Writer literals f builds and
// the file:line of each one missing WriteTimeout or ReadTimeout.
func writersWithoutTimeouts(fset *token.FileSet, f *ast.File) (writers int, missing []string) {
	alias := acksKafkaAlias(f)
	if alias == "" || alias == "_" {
		return 0, nil
	}
	ast.Inspect(f, func(n ast.Node) bool {
		cl, ok := n.(*ast.CompositeLit)
		if !ok || cl.Type == nil || !isKafkaWriterType(cl.Type, alias) {
			return true
		}
		writers++
		for _, key := range []string{"WriteTimeout", "ReadTimeout"} {
			if !hasKey(cl, key) {
				missing = append(missing, fset.Position(cl.Pos()).String()+" missing "+key)
			}
		}
		return true
	})
	return writers, missing
}

func timeoutViolation(pos string) string {
	return fmt.Sprintf("ARCHITECTURE VIOLATION (contents rule)\n"+
		"  RULE:    every kafka-go Writer sets WriteTimeout and ReadTimeout explicitly\n"+
		"  WHAT:    %s\n"+
		"  WHY:     with RequireAll and no deadline, one half-open broker connection blocks WriteMessages forever while\n"+
		"           the outbox relay holds its row locks: passes go silent (no error) and later events rot unpublished\n"+
		"           (material-flow-control e2e incident, 2026-11-20).\n"+
		"  FIX:     set WriteTimeout and ReadTimeout (10s is the fleet value) so a stalled write fails, the row's\n"+
		"           failure is recorded, and the next pass re-dials. Do NOT loosen this test.\n", pos)
}

func TestKafkaWritersSetTimeouts(t *testing.T) {
	total := 0
	for _, p := range acksSourceFiles(t, "internal", "cmd") {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, p, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", p, err)
		}
		writers, missing := writersWithoutTimeouts(fset, f)
		total += writers
		for _, pos := range missing {
			t.Errorf("%s", timeoutViolation(pos))
		}
	}
	if total == 0 {
		if !moduleRequiresKafkaGo(t) {
			t.Skip("module does not require github.com/segmentio/kafka-go yet: no Kafka writer to guard")
		}
		t.Fatal("no kafka-go Writer literal found under internal/ or cmd/; this service writes to Kafka, " +
			"so the sensor is looking in the wrong place")
	}
}

func TestKafkaWriterTimeoutSensorBites(t *testing.T) {
	cases := []struct {
		name        string
		src         string
		wantWriters int
		wantMissing int
	}{
		{"writer without timeouts is flagged twice", `package x
import kafkago "github.com/segmentio/kafka-go"
func f() any { return &kafkago.Writer{Addr: kafkago.TCP("b"), RequiredAcks: kafkago.RequireAll} }`, 1, 2},
		{"write timeout alone misses read", `package x
import kafkago "github.com/segmentio/kafka-go"
func f() any { return &kafkago.Writer{Addr: kafkago.TCP("b"), RequiredAcks: kafkago.RequireAll, WriteTimeout: 1} }`, 1, 1},
		{"writer with both timeouts passes", `package x
import kafkago "github.com/segmentio/kafka-go"
func f() any { return &kafkago.Writer{Addr: kafkago.TCP("b"), RequiredAcks: kafkago.RequireAll, WriteTimeout: 1, ReadTimeout: 1} }`, 1, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, "src.go", tc.src, 0)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			writers, missing := writersWithoutTimeouts(fset, f)
			if writers != tc.wantWriters || len(missing) != tc.wantMissing {
				t.Fatalf("writers=%d missing=%v, want %d writer(s) and %d missing", writers, missing, tc.wantWriters, tc.wantMissing)
			}
		})
	}
}
