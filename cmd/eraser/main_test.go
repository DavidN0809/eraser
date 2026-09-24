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

func TestDiscoveryCLIPreviewNeverContactsProvider(t *testing.T) {
	d := t.TempDir()
	t.Setenv("ERASER_DATA_DIR", d)
	t.Setenv("ERASER_ENABLE_DISCOVERY", "true")
	t.Setenv("ERASER_ENABLE_SEND", "true")
	cfg := filepath.Join(d, "config.yaml")
	catalog := filepath.Join(d, "brokers.yaml")
	if err := os.WriteFile(cfg, []byte("profile:\n  first_name: Synthetic\n  last_name: Person\n  email: NEVER-DISCLOSE@example.invalid\ndiscovery:\n  fields: [name]\n  api_key_file: /missing/secret\noptions:\n  dry_run: false\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(catalog, []byte("brokers:\n  - id: fixture\n    name: Fixture\n    website: https://broker.example.invalid\n    email: broker@example.invalid\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"discover", "--broker", "fixture"}, {"discover", "--broker", "fixture", "--dry-run", "--approve-sha256", "bad"}} {
		c := command()
		var out bytes.Buffer
		c.SetOut(&out)
		c.SetArgs(append([]string{"--config", cfg, "--brokers", catalog}, args...))
		if err := c.Execute(); err != nil {
			t.Fatal("preview tried loading transport secret", err)
		}
		if !strings.Contains(out.String(), "SEARCH PREVIEW ONLY") || strings.Contains(out.String(), "NEVER-DISCLOSE") {
			t.Fatal("query leaked unselected fields")
		}
	}
}
