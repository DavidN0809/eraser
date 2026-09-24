package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIDryRunCannotSendEvenWhenEnabled(t *testing.T) {
	d := t.TempDir()
	t.Setenv("ERASER_DATA_DIR", d)
	t.Setenv("ERASER_ENABLE_SEND", "true")
	cfg := filepath.Join(d, "config.yaml")
	catalog := filepath.Join(d, "brokers.yaml")
	if e := os.WriteFile(cfg, []byte(`profile:
  first_name: Synthetic
  phone: NEVER-DISCLOSE
email:
  from: sender@example.invalid
options:
  dry_run: false
  approved_brokers:
    fixture:
      email: broker@example.invalid
      fields: [name]
`), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(catalog, []byte("brokers:\n  - id: fixture\n    name: Fixture\n    email: broker@example.invalid\n"), 0600); e != nil {
		t.Fatal(e)
	}
	c := command()
	var out bytes.Buffer
	c.SetOut(&out)
	c.SetArgs([]string{"--config", cfg, "--brokers", catalog, "send", "--broker", "fixture", "--dry-run", "--approve-sha256", "bad"})
	if e := c.Execute(); e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(out.String(), "PREVIEW ONLY") || strings.Contains(out.String(), "NEVER-DISCLOSE") {
		t.Fatal(out.String())
	}
}
