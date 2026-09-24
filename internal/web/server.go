package web

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/eraser-privacy/eraser/internal/config"
	"github.com/eraser-privacy/eraser/internal/email"
	"github.com/eraser-privacy/eraser/internal/service"
)

type approval struct {
	ID      string
	Message email.Message
	Expires time.Time
}
type Server struct {
	Service         *service.Service
	token           string
	origin          string
	csrf            string
	mu              sync.Mutex
	approvals       map[string]approval
	searchApprovals map[string]searchApproval
	server          *http.Server
}

func TokenPath() string {
	if p := os.Getenv("ERASER_AUTH_TOKEN_FILE"); p != "" {
		return p
	}
	return filepath.Join(config.DataDir(), "web-token")
}
func New(svc *service.Service) (*Server, error) {
	token, err := config.ReadSecret(TokenPath())
	if err != nil {
		if _, statErr := os.Stat(TokenPath()); !os.IsNotExist(statErr) || os.Getenv("ERASER_AUTH_TOKEN_FILE") != "" {
			return nil, err
		}
		if err = os.MkdirAll(config.DataDir(), 0700); err != nil {
			return nil, err
		}
		token, err = randomToken()
		if err != nil {
			return nil, err
		}
		f, e := os.OpenFile(TokenPath(), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return nil, e
		}
		_, e = f.WriteString(token + "\n")
		closeErr := f.Close()
		if e != nil {
			return nil, e
		}
		if closeErr != nil {
			return nil, closeErr
		}
	}
	if len(token) < 32 {
		return nil, fmt.Errorf("authentication token must contain at least 32 bytes")
	}
	origin := os.Getenv("ERASER_PUBLIC_ORIGIN")
	if origin == "" {
		origin = "http://localhost:8080"
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("ERASER_PUBLIC_ORIGIN must be an exact http(s) origin without trailing slash")
	}
	if u.Scheme == "http" && u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1" && u.Hostname() != "::1" {
		return nil, fmt.Errorf("non-loopback public origins require HTTPS")
	}
	csrf, err := randomToken()
	if err != nil {
		return nil, err
	}
	return &Server{Service: svc, token: token, origin: origin, csrf: csrf, approvals: map[string]approval{}, searchApprovals: map[string]searchApproval{}}, nil
}
func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		if r.URL.Path == "/healthz" && r.Method == http.MethodGet {
			w.WriteHeader(http.StatusOK)
			return
		}
		origin, _ := url.Parse(s.origin)
		if r.Host != origin.Host {
			http.Error(w, "invalid host", http.StatusForbidden)
			return
		}
		user, password, ok := r.BasicAuth()
		got, want := sha256.Sum256([]byte(password)), sha256.Sum256([]byte(s.token))
		if !ok || user != "eraser" || subtle.ConstantTimeCompare(got[:], want[:]) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="Eraser", charset="UTF-8"`)
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
		if r.Method == http.MethodPost {
			if r.Header.Get("Origin") != "" && r.Header.Get("Origin") != s.origin {
				http.Error(w, "invalid origin", http.StatusForbidden)
				return
			}
			if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
				http.Error(w, "cross-site request denied", http.StatusForbidden)
				return
			}
			if err := r.ParseForm(); err != nil {
				http.Error(w, "invalid form", http.StatusBadRequest)
				return
			}
			if subtle.ConstantTimeCompare([]byte(r.PostForm.Get("csrf")), []byte(s.csrf)) != 1 {
				http.Error(w, "invalid CSRF token", http.StatusForbidden)
				return
			}
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/":
			s.dashboard(w, r)
		case r.Method == http.MethodPost && r.URL.Path == "/discovery/preview":
			s.discoveryPreview(w, r)
		case r.Method == http.MethodPost && r.URL.Path == "/discovery/search":
			s.discover(w, r)
		case r.Method == http.MethodPost && r.URL.Path == "/discovery/review":
			s.reviewMatch(w, r)
		case r.Method == http.MethodPost && r.URL.Path == "/discovery/forget":
			s.forgetDiscovery(w, r)
		case r.Method == http.MethodPost && r.URL.Path == "/preview":
			s.preview(w, r)
		case r.Method == http.MethodPost && r.URL.Path == "/send":
			s.send(w, r)
		default:
			http.NotFound(w, r)
		}
	})
}
func (s *Server) Start(addr string) error {
	s.server = &http.Server{Addr: addr, Handler: s.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 40 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	err := s.server.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}
func (s *Server) Shutdown(ctx context.Context) error {
	if s.server == nil {
		return nil
	}
	return s.server.Shutdown(ctx)
}
func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	records, err := s.Service.History.Recent(100)
	if err != nil {
		http.Error(w, "cannot read history", http.StatusInternalServerError)
		return
	}
	matches, err := s.Service.History.Matches()
	if err != nil {
		http.Error(w, "cannot read discovery", 500)
		return
	}
	scans, err := s.Service.History.Scans()
	if err != nil {
		http.Error(w, "cannot read scans", 500)
		return
	}
	data := map[string]any{"Matches": matches, "Scans": scans, "Dashboard": true, "CSRF": s.csrf, "Brokers": s.Service.Brokers.Brokers, "History": records, "PreviewOnly": s.Service.Config.Options.DryRun || os.Getenv("ERASER_ENABLE_SEND") != "true"}
	s.render(w, data)
}
func (s *Server) preview(w http.ResponseWriter, r *http.Request) {
	id := r.PostForm.Get("broker")
	msg, err := s.Service.Preview(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	nonce, err := randomToken()
	if err != nil {
		http.Error(w, "cannot create preview", http.StatusInternalServerError)
		return
	}
	s.mu.Lock()
	for k, a := range s.approvals {
		if time.Now().After(a.Expires) {
			delete(s.approvals, k)
		}
	}
	if len(s.approvals) >= 100 {
		s.mu.Unlock()
		http.Error(w, "too many pending previews", http.StatusTooManyRequests)
		return
	}
	s.approvals[nonce] = approval{id, msg, time.Now().Add(5 * time.Minute)}
	s.mu.Unlock()
	s.render(w, map[string]any{"CSRF": s.csrf, "Message": msg, "Approval": nonce, "PreviewOnly": s.Service.Config.Options.DryRun || os.Getenv("ERASER_ENABLE_SEND") != "true"})
}
func (s *Server) send(w http.ResponseWriter, r *http.Request) {
	if r.PostForm.Get("confirm") != "yes" {
		http.Error(w, "explicit confirmation required", http.StatusBadRequest)
		return
	}
	nonce := r.PostForm.Get("approval")
	s.mu.Lock()
	a, ok := s.approvals[nonce]
	delete(s.approvals, nonce)
	s.mu.Unlock()
	if !ok || time.Now().After(a.Expires) {
		http.Error(w, "preview expired or already used", http.StatusConflict)
		return
	}
	msg, err := s.Service.Preview(a.ID)
	if err != nil || msg != a.Message {
		http.Error(w, "recipient or message changed; preview again", http.StatusConflict)
		return
	}
	result, err := s.Service.Send(r.Context(), a.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !result.Success {
		http.Error(w, result.Error.Error(), http.StatusBadGateway)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
func (s *Server) render(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := page.Execute(w, data); err != nil {
		return
	}
}

var page = template.Must(template.New("page").Parse(`<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width"><title>Eraser</title><style>body{font:17px system-ui;max-width:980px;margin:3rem auto;padding:0 1rem;color:#172c27;background:#f5faf8}a{color:#17664f}table{width:100%;border-collapse:collapse}td,th{text-align:left;padding:.6rem;border-bottom:1px solid #ccd9d3}button{padding:.6rem;background:#17664f;color:white;border:0;border-radius:.3rem}pre{white-space:pre-wrap;background:white;padding:1rem;border:1px solid #ccd9d3}.notice{padding:1rem;background:#e4efe8}</style><h1><a href="/">Eraser</a></h1><p class="notice">{{if .PreviewOnly}}Preview mode — email delivery is disabled.{{else}}Live delivery enabled. Every send requires a fresh preview and confirmation.{{end}}</p><p>Removal requires a discovered candidate that you have confirmed belongs to you. Only explicitly approved broker IDs and email addresses can receive a request. Review whether a broker already holds your data. Even a minimal email reveals the sender address and may create a new record.</p>{{if .Notice}}<p class="notice">{{.Notice}}</p>{{end}}
{{if .SearchPlan}}<h2>Review this active search</h2><p>Provider: Brave Search API. This sends the exact query below to Brave. It does not contact the broker or send mail. Search terms and results may be sensitive; provider retention depends on your plan.</p><pre>{{.SearchPlan.Query}}</pre><p>Broker: {{.SearchPlan.BrokerID}}. Results may be incorrect, stale or incomplete. Existing evidence for this broker is replaced after a successful search.</p>{{if .DiscoveryEnabled}}<form method="post" action="/discovery/search"><input type="hidden" name="csrf" value="{{.CSRF}}"><input type="hidden" name="approval" value="{{.SearchApproval}}"><label><input type="checkbox" name="confirm" value="yes" required> I approve disclosing these search terms to Brave.</label><p><button>Run this search</button></p></form>{{else}}<p>Active discovery is disabled. Configure a search API secret and ERASER_ENABLE_DISCOVERY=true to enable it.</p>{{end}}{{end}}
{{if .Message}}<h2>Review this exact request</h2><p><b>From:</b> {{.Message.From}}<br><b>To:</b> {{.Message.To}}<br><b>Subject:</b> {{.Message.Subject}}</p><pre>{{.Message.Body}}</pre>{{if not .PreviewOnly}}<form method="post" action="/send"><input type="hidden" name="csrf" value="{{.CSRF}}"><input type="hidden" name="approval" value="{{.Approval}}"><label><input type="checkbox" name="confirm" value="yes" required> I approve this recipient and every field shown above.</label><p><button>Send this request</button></p></form>{{end}}{{end}}{{if .Dashboard}}<h2>Discovery</h2><p>Start with a search preview for a selected broker below. Confirm only candidates you have reviewed and believe describe you. Search snippets are untrusted evidence, not proof. Result URLs are shown as text; opening one yourself contacts that site. Evidence expires after 30 days. Changing your profile, search fields or broker target requires a fresh search.</p>
{{range .Matches}}<article><h3>{{.BrokerID}} — {{.Status}}</h3><p>{{.Title}}</p><pre>{{.URL}}</pre><p>{{.Snippet}}</p><form method="post" action="/discovery/review"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="match" value="{{.ID}}"><button name="decision" value="confirmed">I reviewed this match: it is me</button> <button name="decision" value="rejected">Not me / revoke confirmation</button></form></article>{{else}}<p>No current candidates. Search failures or missing results do not establish that your data is absent.</p>{{end}}
<table><tr><th>Broker</th><th>Last search (UTC)</th><th>Candidates</th><th>Evidence</th></tr>{{range .Scans}}<tr><td>{{.BrokerID}}</td><td>{{.CreatedAt}}</td><td>{{.Candidates}}</td><td><form method="post" action="/discovery/forget"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="broker" value="{{.BrokerID}}"><button>Delete evidence and revoke confirmation</button></form></td></tr>{{end}}</table>
<h2>Broker catalog</h2><p>The catalog is a list of unverified leads, not proof that a broker holds your information. Approve exact recipients and disclosure fields in your private configuration, then restart to load changes.</p><table><tr><th>Broker</th><th>Recipient</th><th>Action</th></tr>{{range .Brokers}}<tr><td>{{.Name}}</td><td>{{.Email}}</td><td><form method="post" action="/discovery/preview"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="broker" value="{{.ID}}"><button>Preview search</button></form><form method="post" action="/preview"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="broker" value="{{.ID}}"><button>Preview removal</button></form></td></tr>{{end}}</table><h2>Delivery history</h2><p>An attempted record without a final result means delivery is uncertain. Check your mail provider before retrying.</p><table><tr><th>Time</th><th>Broker</th><th>Result</th></tr>{{range .History}}<tr><td>{{.SentAt}}</td><td>{{.BrokerID}}</td><td>{{.Status}}</td></tr>{{else}}<tr><td>No delivery attempts.</td></tr>{{end}}</table>{{end}}</html>`))
