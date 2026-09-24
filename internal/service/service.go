// Package service is the sole broker-delivery policy boundary for CLI and HTTP.
package service

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/eraser-privacy/eraser/internal/broker"
	"github.com/eraser-privacy/eraser/internal/config"
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
