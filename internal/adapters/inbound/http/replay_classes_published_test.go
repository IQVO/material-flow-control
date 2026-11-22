package http_test

import (
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	mfchttp "github.com/IQVO/material-flow-control/internal/adapters/inbound/http"
)

var slugRe = regexp.MustCompile("`([a-z]+(?:-[a-z]+)+)`")

// carry (Task 18 review): the replay classes the contract and the docs publish are the catalogue's, slug for slug:
// the "Replay classes" paragraph of apis/openapi.yaml info.description and the code table of
// docs/docs/development/replay-classes.md (status and class).
func TestThePublishedReplayClassesEqualTheCatalogue(t *testing.T) {
	want := map[string]string{}
	status := map[string]int{}
	for _, e := range mfchttp.Catalogue() {
		want[e.Slug], status[e.Slug] = strings.ToUpper(e.Class), e.Status
	}
	if got := openAPIReplayClasses(t); !equalClasses(got, want) {
		t.Fatalf("apis/openapi.yaml replay classes = %v, want the catalogue %v", got, want)
	}
	gotDoc, gotStatus := docReplayClasses(t)
	if !equalClasses(gotDoc, want) {
		t.Fatalf("replay-classes.md classes = %v, want the catalogue %v", gotDoc, want)
	}
	for slug, s := range status {
		if gotStatus[slug] != s {
			t.Fatalf("replay-classes.md: %s status %d, want %d", slug, gotStatus[slug], s)
		}
	}
}

func equalClasses(got, want map[string]string) bool {
	if len(got) != len(want) {
		return false
	}
	for k, v := range want {
		if got[k] != v {
			return false
		}
	}
	return true
}

// openAPIReplayClasses reads "FINAL — … ; OUTCOME — … ; RETRYABLE — …" from the info description: every backticked
// kebab-case slug belongs to the class whose marker precedes it.
func openAPIReplayClasses(t *testing.T) map[string]string {
	t.Helper()
	raw, err := os.ReadFile("../../../../apis/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Info struct {
			Description string `yaml:"description"`
		} `yaml:"info"`
	}
	if err := yaml.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	d := strings.Join(strings.Fields(spec.Info.Description), " ")
	start := strings.Index(d, "**Replay classes**")
	end := strings.Index(d, "2xx answers are stored")
	if start < 0 || end < start {
		t.Fatalf("no Replay classes paragraph in the info description")
	}
	para := d[start:end]
	marks := []string{"FINAL —", "OUTCOME —", "RETRYABLE —"}
	out := map[string]string{}
	for i, m := range marks {
		from := strings.Index(para, m)
		if from < 0 {
			t.Fatalf("no %q in the Replay classes paragraph", m)
		}
		to := len(para)
		if i+1 < len(marks) {
			to = strings.Index(para, marks[i+1])
		}
		for _, s := range slugRe.FindAllStringSubmatch(para[from:to], -1) {
			out[s[1]] = strings.TrimSuffix(m, " —")
		}
	}
	return out
}

// docReplayClasses reads the "| `slug` | status | CLASS |" rows of the REST table (the agent-only table has a Surface
// column instead of a class and is skipped).
func docReplayClasses(t *testing.T) (map[string]string, map[string]int) {
	t.Helper()
	raw, err := os.ReadFile("../../../../docs/docs/development/replay-classes.md")
	if err != nil {
		t.Fatal(err)
	}
	classes, statuses := map[string]string{}, map[string]int{}
	for _, line := range strings.Split(string(raw), "\n") {
		cells := strings.Split(line, "|")
		if len(cells) < 5 {
			continue
		}
		slug, class := strings.Trim(strings.TrimSpace(cells[1]), "`"), strings.TrimSpace(cells[3])
		n, err := strconv.Atoi(strings.TrimSpace(cells[2]))
		if err != nil || !slices.Contains([]string{"FINAL", "OUTCOME", "RETRYABLE"}, class) {
			continue
		}
		classes[slug], statuses[slug] = class, n
	}
	return classes, statuses
}
