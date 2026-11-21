package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/IQVO/material-flow-control/internal/config"
	"github.com/IQVO/material-flow-control/internal/domain/layout"
)

// carry (Task 2 review): an empty layout file (no bytes, only whitespace, only comments) is "layout is empty",
// never the decoder's bare EOF, and every LoadLayout error names the file it read.
func TestAnEmptyLayoutIsRefusedAsEmpty(t *testing.T) {
	for name, body := range map[string]string{"no bytes": "", "whitespace": "  \n\t\n", "comments only": "# nothing configured\n"} {
		t.Run(name, func(t *testing.T) {
			_, err := config.ParseLayout([]byte(body))
			if !errors.Is(err, layout.ErrInvalid) || !strings.Contains(err.Error(), "layout is empty") || strings.Contains(err.Error(), "EOF") {
				t.Fatalf("ParseLayout(%q) = %v, want ErrInvalid \"layout is empty\" without EOF", body, err)
			}
		})
	}
}

func TestEveryLoadLayoutErrorNamesTheFile(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{"empty": "", "malformed": "siteId: [\n", "unknown key": "siteId: WH1\nbogus: 1\n",
		"invalid": "siteId: WH1\n"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(dir, strings.ReplaceAll(name, " ", "-")+".yaml")
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := config.LoadLayout(path)
			if err == nil || !strings.Contains(err.Error(), path) {
				t.Fatalf("LoadLayout(%s) = %v, want an error naming the file", path, err)
			}
		})
	}
	_, err := config.LoadLayout(filepath.Join(dir, "invalid.yaml"))
	if !errors.Is(err, layout.ErrInvalid) {
		t.Fatalf("the wrapped validation error = %v, want ErrInvalid kept", err)
	}
}
