package web

import (
	"context"
	"github.com/eraser-privacy/eraser/internal/broker"
	"github.com/eraser-privacy/eraser/internal/config"
	"github.com/eraser-privacy/eraser/internal/history"
	"github.com/eraser-privacy/eraser/internal/service"
	templates "github.com/eraser-privacy/eraser/internal/template"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T) *Server {
	t.Helper()
	d := t.TempDir()
	t.Setenv("ERASER_DATA_DIR", d)
	t.Setenv("ERASER_AUTH_TOKEN_FILE", "")
	t.Setenv("ERASER_PUBLIC_ORIGIN", "http://localhost:8080")
	t.Setenv("ERASER_ENABLE_SEND", "false")
	h, e := history.NewStore(filepath.Join(d, "history.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = h.Close() })
	eng, e := templates.NewEngine()
	if e != nil {
		t.Fatal(e)
	}
	cfg := &config.Config{Profile: config.Profile{FirstName: "Synthetic", LastName: "Person", Email: "person@example.invalid", Phone: "SENSITIVE-PHONE", DateOfBirth: "SENSITIVE-DOB", Address: "SENSITIVE-ADDRESS"}, Email: config.EmailConfig{From: "sender@example.invalid"}, Options: config.Options{DryRun: true, Template: "generic", ApprovedBrokers: map[string]config.Approval{"test": {Email: "broker@example.invalid", Fields: []string{"name"}}}}}
	s, e := New(&service.Service{Config: cfg, History: h, Engine: eng, Brokers: &broker.BrokerDatabase{Brokers: []broker.Broker{{ID: "test", Name: "<script>alert(1)</script>", Email: "broker@example.invalid"}}}})
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func request(s *Server, method, path string, form url.Values, auth bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://localhost:8080"+path, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if auth {
		r.SetBasicAuth("eraser", s.token)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}
func TestAuthHostAndCSRF(t *testing.T) {
	s := fixture(t)
	if w := request(s, "GET", "/", nil, false); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := request(s, "GET", "/healthz", nil, false); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := request(s, "POST", "/preview", url.Values{"broker": {"test"}}, true); w.Code != 403 {
		t.Fatal(w.Code)
	}
	for _, origin := range []string{"https://evil.invalid", "http://localhost:9999"} {
		r := httptest.NewRequest("POST", "http://localhost:8080/preview", strings.NewReader(url.Values{"csrf": {s.csrf}, "broker": {"test"}}.Encode()))
		r.SetBasicAuth("eraser", s.token)
		r.Header.Set("Origin", origin)
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatal(w.Code)
		}
	}
	r := httptest.NewRequest("GET", "http://evil.invalid/", nil)
	r.SetBasicAuth("eraser", s.token)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
}
func TestPreviewMinimizesEscapesAndDoesNotSend(t *testing.T) {
	s := fixture(t)
	w := request(s, "POST", "/preview", url.Values{"csrf": {s.csrf}, "broker": {"test"}}, true)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	for _, secret := range []string{"SENSITIVE-PHONE", "SENSITIVE-DOB", "SENSITIVE-ADDRESS", "person@example.invalid", "<script>", s.token} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatalf("disclosed %s", secret)
		}
	}
	if !strings.Contains(w.Body.String(), "Synthetic Person") {
		t.Fatal("approved name missing")
	}
	records, e := s.Service.History.Recent(100)
	if e != nil || len(records) != 0 {
		t.Fatal("preview recorded delivery")
	}
	for nonce := range s.approvals {
		v := url.Values{"csrf": {s.csrf}, "approval": {nonce}, "confirm": {"yes"}}
		if w = request(s, "POST", "/send", v, true); w.Code != 400 {
			t.Fatal(w.Code)
		}
		if w = request(s, "POST", "/send", v, true); w.Code != 409 {
			t.Fatal("replay accepted")
		}
	}
}
func TestExactRecipientAndExclusion(t *testing.T) {
	s := fixture(t)
	s.Service.Brokers.Brokers[0].Email = "changed@example.invalid"
	if _, e := s.Service.Preview("test"); e == nil {
		t.Fatal("recipient change accepted")
	}
	s.Service.Brokers.Brokers[0].Email = "broker@example.invalid"
	s.Service.Config.Options.ExcludedBrokers = []string{"test"}
	if _, e := s.Service.Preview("test"); e == nil {
		t.Fatal("excluded broker accepted")
	}
}
func TestPreviewInvalidatedByConfigChange(t *testing.T) {
	s := fixture(t)
	request(s, "POST", "/preview", url.Values{"csrf": {s.csrf}, "broker": {"test"}}, true)
	s.Service.Config.Profile.FirstName = "Changed"
	for nonce := range s.approvals {
		w := request(s, "POST", "/send", url.Values{"csrf": {s.csrf}, "approval": {nonce}, "confirm": {"yes"}}, true)
		if w.Code != 409 {
			t.Fatal("changed body accepted")
		}
	}
}
func TestNoPendingJobResume(t *testing.T) {
	s := fixture(t)
	if e := os.WriteFile(filepath.Join(config.DataDir(), "pending_job.json"), []byte(`{"remaining_brokers":["test"]}`), 0600); e != nil {
		t.Fatal(e)
	}
	_, e := New(s.Service)
	if e != nil {
		t.Fatal(e)
	}
	rows, e := s.Service.History.Recent(100)
	if e != nil || len(rows) != 0 {
		t.Fatal("restart sent mail")
	}
	_ = s.Shutdown(context.Background())
}
