package discovery

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eraser-privacy/eraser/internal/broker"
	"github.com/eraser-privacy/eraser/internal/config"
)

func fixturePlan(t *testing.T) (*config.Config, Plan) {
	t.Helper()
	cfg := &config.Config{Profile: config.Profile{FirstName: "Synthetic", LastName: "Person", City: "Testville", Email: "private@example.invalid", Address: "NEVER-ADDRESS", DateOfBirth: "NEVER-DOB", Phone: "NEVER-PHONE"}, Discovery: config.DiscoveryConfig{Fields: []string{"name", "city"}}}
	plan, err := BuildPlan(cfg, broker.Broker{ID: "fixture", Website: "https://broker.example.invalid", Email: "privacy@example.invalid"})
	if err != nil {
		t.Fatal(err)
	}
	return cfg, plan
}
func TestPlanMinimizationAndBinding(t *testing.T) {
	cfg, p := fixturePlan(t)
	if p.Query != `site:broker.example.invalid "Synthetic Person" "Testville"` {
		t.Fatal(p.Query)
	}
	b := broker.Broker{ID: "fixture", Website: "https://broker.example.invalid", Email: "privacy@example.invalid"}
	for _, mutate := range []func(){func() { cfg.Profile.Email = "changed@example.invalid" }, func() { b.Email = "changed@example.invalid" }, func() { b.Website = "https://changed.example.invalid" }, func() { cfg.Discovery.Fields = []string{"email"} }} {
		mutate()
		next, err := BuildPlan(cfg, b)
		if err != nil || next.Fingerprint == p.Fingerprint {
			t.Fatal("evidence not bound to changed profile/target/query", err)
		}
	}
	for _, field := range []string{"address", "date_of_birth", "unknown"} {
		cfg.Discovery.Fields = []string{field}
		if _, err := BuildPlan(cfg, b); err == nil {
			t.Fatal("unsupported disclosure")
		}
	}
	cfg.Discovery.Fields = []string{"name"}
	cfg.Profile.FirstName = `Synthetic" OR site:evil.invalid`
	if _, err := BuildPlan(cfg, b); err == nil {
		t.Fatal("query syntax injection")
	}
	cfg.Profile.FirstName = "Synthetic"
	for _, website := range []string{"https://127.0.0.1", "https://user:pass@broker.example.invalid", "https://broker.example.invalid:443", "http://localhost", "file:///etc/passwd"} {
		b.Website = website
		if _, err := BuildPlan(cfg, b); err == nil {
			t.Fatal("unsafe domain accepted", website)
		}
	}
}

type rewriteTransport struct {
	target string
	base   http.RoundTripper
	t      *testing.T
}

func (r rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.String() != Endpoint {
		r.t.Fatal("unexpected production destination")
	}
	clone := req.Clone(req.Context())
	u := *req.URL
	clone.URL = &u
	target := strings.TrimPrefix(r.target, "https://")
	clone.URL.Host = target
	clone.Host = target
	return r.base.RoundTrip(clone)
}
func TestSearchLocalTLSFilteringAndNoBrokerFetch(t *testing.T) {
	_, plan := fixturePlan(t)
	calls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "POST" || r.URL.RawQuery != "" || r.Header.Get("X-Subscription-Token") != "synthetic-key" {
			t.Error("incorrect request")
		}
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "NEVER") || strings.Contains(string(body), "private@example.invalid") {
			t.Error("unselected PII disclosed")
		}
		var query map[string]any
		if err := json.Unmarshal(body, &query); err != nil {
			t.Error(err)
		}
		if query["q"] != plan.Query {
			t.Error("query changed")
		}
		_, _ = io.WriteString(w, `{"web":{"results":[{"url":"https://broker.example.invalid/profile/1","title":"<script>bad()</script>","description":"Synthetic Person"},{"url":"https://broker.example.invalid.evil.invalid/a"},{"url":"http://broker.example.invalid/a"},{"url":"https://user@broker.example.invalid/a"},{"url":"https://127.0.0.1/a"},{"url":"https://sub.broker.example.invalid/profile/2"},{"url":"https://broker.example.invalid/profile/1#duplicate"}]}}`)
	}))
	defer server.Close()
	client := server.Client()
	client.Transport = rewriteTransport{server.URL, client.Transport, t}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	matches, err := search(context.Background(), client, "synthetic-key", plan)
	if err != nil || len(matches) != 2 || calls != 1 {
		t.Fatalf("unexpected results/calls %v %d %d", err, len(matches), calls)
	}
	for _, m := range matches {
		if m.Status != "pending" {
			t.Fatal("auto-confirmed result")
		}
	}
}
func TestProviderFailuresBoundedAndRedacted(t *testing.T) {
	_, plan := fixturePlan(t)
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{{"redirect", 302, ""}, {"quota", 429, "SECRET-QUERY-KEY"}, {"oversize", 200, strings.Repeat("x", (1<<20)+1)}, {"malformed", 200, "SECRET-QUERY-KEY"}} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Location", "https://127.0.0.1/private")
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			client := server.Client()
			client.Transport = rewriteTransport{server.URL, client.Transport, t}
			client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
			_, err := search(context.Background(), client, "synthetic-key", plan)
			if err == nil || strings.Contains(err.Error(), "SECRET") || calls != 1 {
				t.Fatal("failure not contained", err, calls)
			}
		})
	}
}
func TestSearchDisabledOrMissingSecretNeverDials(t *testing.T) {
	cfg, plan := fixturePlan(t)
	t.Setenv("ERASER_ENABLE_DISCOVERY", "false")
	if _, err := Search(context.Background(), cfg, plan); err == nil {
		t.Fatal("disabled search accepted")
	}
	t.Setenv("ERASER_ENABLE_DISCOVERY", "true")
	cfg.Discovery.APIKeyFile = filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(cfg.Discovery.APIKeyFile, []byte(""), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Search(context.Background(), cfg, plan); err == nil {
		t.Fatal("empty secret accepted")
	}
}
