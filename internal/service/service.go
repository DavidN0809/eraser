// Package service is the sole broker-delivery policy boundary for CLI and HTTP.
package service

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/eraser-privacy/eraser/internal/broker"
	"github.com/eraser-privacy/eraser/internal/config"
	"github.com/eraser-privacy/eraser/internal/discovery"
	"github.com/eraser-privacy/eraser/internal/email"
	"github.com/eraser-privacy/eraser/internal/history"
	templates "github.com/eraser-privacy/eraser/internal/template"
)

type Service struct {
	Config      *config.Config
	Brokers     *broker.BrokerDatabase
	History     *history.Store
	Engine      *templates.Engine
	mu          sync.Mutex
	lastAttempt time.Time
	lastSearch  time.Time
}

func (s *Service) Preview(id string) (email.Message, error) {
	b := s.Brokers.FindByID(id)
	if b == nil {
		return email.Message{}, fmt.Errorf("unknown broker")
	}
	if !s.Config.Approved(b.ID, b.Email) {
		return email.Message{}, fmt.Errorf("broker ID and recipient must be explicitly approved in configuration")
	}
	eligible := false
	for _, allowed := range s.Brokers.Filter(s.Config.Options.Regions, s.Config.Options.ExcludedBrokers) {
		if allowed.ID == b.ID {
			eligible = true
		}
	}
	if !eligible {
		return email.Message{}, fmt.Errorf("broker excluded by policy")
	}
	rendered, err := s.Engine.Render(s.Config.Options.Template, s.Config.Disclosure(id), *b)
	if err != nil {
		return email.Message{}, err
	}
	return email.Message{To: b.Email, From: s.Config.Email.From, Subject: rendered.Subject, Body: rendered.Body}, nil
}
func (s *Service) Send(ctx context.Context, id string) (email.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	msg, err := s.Preview(id)
	if err != nil {
		return email.Result{}, err
	}
	plan, err := s.DiscoveryPlan(id)
	if err != nil {
		return email.Result{}, err
	}
	confirmed, err := s.History.HasConfirmed(plan)
	if err != nil || !confirmed {
		return email.Result{}, fmt.Errorf("removal requires a current, manually confirmed discovery match")
	}
	sender, err := email.NewSender(s.Config)
	if err != nil {
		return email.Result{}, err
	}
	if time.Since(s.lastAttempt) < time.Duration(s.Config.Options.RateLimitMs)*time.Millisecond {
		return email.Result{}, fmt.Errorf("please wait before another send")
	}
	if err = ctx.Err(); err != nil {
		return email.Result{}, err
	}
	// An attempted record must persist before contacting SMTP. Crashes never trigger retries.
	if err = s.History.Add(&history.Record{BrokerID: id, Status: "attempted"}); err != nil {
		return email.Result{}, fmt.Errorf("cannot record delivery attempt")
	}
	s.lastAttempt = time.Now()
	result := sender.Send(ctx, msg)
	status := "failed"
	if result.Success {
		status = "sent"
	} else if result.Uncertain {
		status = "uncertain"
	}
	if err = s.History.Add(&history.Record{BrokerID: id, Status: status}); err != nil {
		return result, fmt.Errorf("delivery attempted but result could not be recorded; inspect history before retrying")
	}
	return result, nil
}

// DiscoveryPlan applies region/exclusion policy without requiring permission to send mail.
func (s *Service) DiscoveryPlan(id string) (discovery.Plan, error) {
	for _, b := range s.Brokers.Filter(s.Config.Options.Regions, s.Config.Options.ExcludedBrokers) {
		if b.ID == id {
			return discovery.BuildPlan(s.Config, b)
		}
	}
	return discovery.Plan{}, fmt.Errorf("unknown or excluded broker")
}
func (s *Service) Discover(ctx context.Context, id, digest string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	plan, err := s.DiscoveryPlan(id)
	if err != nil {
		return 0, err
	}
	if digest != plan.Fingerprint {
		return 0, fmt.Errorf("search approval does not match current plan")
	}
	if time.Since(s.lastSearch) < 2*time.Second {
		return 0, fmt.Errorf("please wait before another search")
	}
	s.lastSearch = time.Now()
	if err = s.History.PurgeExpiredDiscovery(); err != nil {
		return 0, fmt.Errorf("cannot expire discovery evidence")
	}
	matches, err := discovery.Search(ctx, s.Config, plan)
	if err != nil {
		return 0, err
	}
	if err = s.History.SaveScan(plan, matches); err != nil {
		return 0, fmt.Errorf("cannot save discovery results")
	}
	return len(matches), nil
}
func (s *Service) ReviewMatch(id, status string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	broker, err := s.History.MatchBroker(id)
	if err != nil {
		return fmt.Errorf("unknown match")
	}
	plan, err := s.DiscoveryPlan(broker)
	if err != nil {
		return err
	}
	return s.History.ReviewMatch(id, plan.Fingerprint, status)
}
func (s *Service) ForgetDiscovery(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.History.ForgetDiscovery(id)
}
