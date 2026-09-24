package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSafeDefaultsAndLegacySecretsRejected(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	for _, body := range []string{"options: {}\n", "email:\n  smtp:\n    password: should-not-be-accepted\n", "profile:\n  unknown: value\n"} {
		if e := os.WriteFile(p, []byte(body), 0600); e != nil {
			t.Fatal(e)
		}
		c, e := Load(p)
		if body == "options: {}\n" {
			if e != nil || !c.Options.DryRun {
				t.Fatal("unsafe default")
			}
		} else if e == nil {
			t.Fatal("unknown/secret field accepted")
		}
	}
}
func TestPrivateFilesRequired(t *testing.T) {
	p := filepath.Join(t.TempDir(), "secret")
	if e := os.WriteFile(p, []byte("synthetic\n"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := ReadSecret(p); e != nil {
		t.Fatal(e)
	}
	if e := os.Chmod(p, 0644); e != nil {
		t.Fatal(e)
	}
	if _, e := ReadSecret(p); e == nil {
		t.Fatal("world-readable secret accepted")
	}
	if _, e := Load(p); e == nil {
		t.Fatal("world-readable config accepted")
	}
}
