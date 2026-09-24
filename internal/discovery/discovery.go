// Package discovery searches an index; it never visits broker or result URLs.
package discovery

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/eraser-privacy/eraser/internal/broker"
	"github.com/eraser-privacy/eraser/internal/config"
)

const Endpoint = "https://api.search.brave.com/res/v1/web/search"
const MaxAge = 30 * 24 * time.Hour

type Plan struct{ BrokerID, Domain, Query, Fingerprint string }
type Match struct{ ID, BrokerID, URL, Title, Snippet, Status string }

var domainPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)+$`)

func BuildPlan(cfg *config.Config, b broker.Broker) (Plan, error) {
	u, err := url.Parse(b.Website)
	if err != nil || u.User != nil || u.Port() != "" || (u.Scheme != "https" && u.Scheme != "http") {
		return Plan{}, fmt.Errorf("broker has no usable website domain")
	}
	domain := strings.ToLower(u.Hostname())
	if len(domain) > 253 || !domainPattern.MatchString(domain) || net.ParseIP(domain) != nil || strings.HasSuffix(domain, ".localhost") || strings.HasSuffix(domain, ".local") {
		return Plan{}, fmt.Errorf("broker has no usable website domain")
	}
	values := map[string]string{"name": cfg.Profile.FullName(), "city": cfg.Profile.City, "state": cfg.Profile.State, "email": cfg.Profile.Email, "phone": cfg.Profile.Phone}
	terms := []string{"site:" + domain}
	seen := map[string]bool{}
	identity := false
	for _, field := range cfg.Discovery.Fields {
		value, ok := values[field]
		if !ok || seen[field] {
			return Plan{}, fmt.Errorf("select unique discovery fields: name, city, state, email or phone")
		}
		seen[field] = true
		value = strings.TrimSpace(value)
		if value == "" || len(value) > 150 {
			return Plan{}, fmt.Errorf("selected discovery field is empty or too long")
		}
		for _, ch := range value {
			if !unicode.IsLetter(ch) && !unicode.IsNumber(ch) && !strings.ContainsRune(" .@+-'()", ch) {
				return Plan{}, fmt.Errorf("selected discovery field contains unsupported search syntax")
			}
		}
		terms = append(terms, `"`+value+`"`)
		identity = identity || field == "name" || field == "email" || field == "phone"
	}
	if !identity {
		return Plan{}, fmt.Errorf("select at least one identifying discovery field: name, email or phone")
	}
	query := strings.Join(terms, " ")
	if len(query) > 600 || len(strings.Fields(query)) > 75 {
		return Plan{}, fmt.Errorf("search query exceeds provider limits")
	}
	// Bind evidence to the full profile, catalog target and exact query. Never log this material.
	material, err := json.Marshal(struct {
		Profile                            config.Profile
		BrokerID, Recipient, Domain, Query string
	}{cfg.Profile, b.ID, b.Email, domain, query})
	if err != nil {
		return Plan{}, fmt.Errorf("cannot construct search plan")
	}
	sum := sha256.Sum256(material)
	return Plan{b.ID, domain, query, hex.EncodeToString(sum[:])}, nil
}

// Search has no configurable endpoint, redirects, environment proxy, cookies, retries or SMTP dependency.
func Search(ctx context.Context, cfg *config.Config, plan Plan) ([]Match, error) {
	if os.Getenv("ERASER_ENABLE_DISCOVERY") != "true" {
		return nil, fmt.Errorf("active discovery disabled; set ERASER_ENABLE_DISCOVERY=true after reviewing privacy implications")
	}
	key, err := config.ReadSecret(cfg.Discovery.APIKeyFile)
	if err != nil || strings.TrimSpace(key) == "" {
		return nil, fmt.Errorf("discovery API key secret is missing or invalid")
	}
	client := newClient()
	defer client.CloseIdleConnections()
	return search(ctx, client, key, plan)
}
func newClient() *http.Client {
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 10 * time.Second, DisableKeepAlives: true}
	return &http.Client{Transport: transport, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func search(ctx context.Context, client *http.Client, key string, plan Plan) ([]Match, error) {
	payload, err := json.Marshal(map[string]any{"q": plan.Query, "count": 20, "result_filter": "web", "text_decorations": false, "spellcheck": false})
	if err != nil {
		return nil, fmt.Errorf("cannot encode search")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, Endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("cannot create search")
	}
	req.Header.Set("X-Subscription-Token", key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("search provider unavailable; no result determined")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("search provider returned HTTP %d; no result determined", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil || len(body) > 1<<20 {
		return nil, fmt.Errorf("search response unreadable or too large")
	}
	var data struct {
		Web *struct {
			Results []struct{ URL, Title, Description string }
		}
	}
	if err = json.Unmarshal(body, &data); err != nil || data.Web == nil {
		return nil, fmt.Errorf("invalid search response")
	}
	matches := []Match{}
	seen := map[string]bool{}
	for _, r := range data.Web.Results {
		if len(matches) == 20 {
			break
		}
		u, e := url.Parse(r.URL)
		if e != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || len(r.URL) > 2048 {
			continue
		}
		host := strings.ToLower(u.Hostname())
		if host != plan.Domain && !strings.HasSuffix(host, "."+plan.Domain) {
			continue
		}
		u.Fragment = ""
		if seen[u.String()] {
			continue
		}
		seen[u.String()] = true
		matches = append(matches, Match{BrokerID: plan.BrokerID, URL: u.String(), Title: clip(r.Title, 300), Snippet: clip(r.Description, 1500), Status: "pending"})
	}
	return matches, nil
}
func clip(s string, n int) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	r := []rune(s)
	if len(r) > n {
		r = r[:n]
	}
	return string(r)
}
