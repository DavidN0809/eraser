package web

import (
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/eraser-privacy/eraser/internal/discovery"
)

type searchApproval struct {
	Plan    discovery.Plan
	Expires time.Time
}

func (s *Server) discoveryPreview(w http.ResponseWriter, r *http.Request) {
	plan, err := s.Service.DiscoveryPlan(r.PostForm.Get("broker"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	nonce, err := randomToken()
	if err != nil {
		http.Error(w, "cannot create search preview", http.StatusInternalServerError)
		return
	}
	s.mu.Lock()
	for k, a := range s.searchApprovals {
		if time.Now().After(a.Expires) {
			delete(s.searchApprovals, k)
		}
	}
	if len(s.searchApprovals) >= 100 {
		s.mu.Unlock()
		http.Error(w, "too many pending searches", http.StatusTooManyRequests)
		return
	}
	s.searchApprovals[nonce] = searchApproval{plan, time.Now().Add(5 * time.Minute)}
	s.mu.Unlock()
	s.render(w, map[string]any{"CSRF": s.csrf, "SearchPlan": plan, "SearchApproval": nonce, "DiscoveryEnabled": os.Getenv("ERASER_ENABLE_DISCOVERY") == "true", "PreviewOnly": s.previewOnly()})
}
func (s *Server) discover(w http.ResponseWriter, r *http.Request) {
	if r.PostForm.Get("confirm") != "yes" {
		http.Error(w, "explicit search disclosure approval required", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	a, ok := s.searchApprovals[r.PostForm.Get("approval")]
	delete(s.searchApprovals, r.PostForm.Get("approval"))
	s.mu.Unlock()
	if !ok || time.Now().After(a.Expires) {
		http.Error(w, "search preview expired or already used", http.StatusConflict)
		return
	}
	n, err := s.Service.Discover(r.Context(), a.Plan.BrokerID, a.Plan.Fingerprint)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.render(w, map[string]any{"Notice": fmt.Sprintf("Search complete: %d candidates. Review Discovery on the home page. Zero results means no indexed candidates were found; it does not prove absence.", n), "PreviewOnly": s.previewOnly()})
}
func (s *Server) reviewMatch(w http.ResponseWriter, r *http.Request) {
	if err := s.Service.ReviewMatch(r.PostForm.Get("match"), r.PostForm.Get("decision")); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
func (s *Server) forgetDiscovery(w http.ResponseWriter, r *http.Request) {
	if err := s.Service.ForgetDiscovery(r.PostForm.Get("broker")); err != nil {
		http.Error(w, "cannot delete discovery evidence", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
func (s *Server) previewOnly() bool {
	return s.Service.Config.Options.DryRun || os.Getenv("ERASER_ENABLE_SEND") != "true"
}
