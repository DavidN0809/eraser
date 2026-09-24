package email

import (
	"context"
	"fmt"
	"github.com/eraser-privacy/eraser/internal/config"
	"os"
	"strings"
)

type Message struct{ To, From, Subject, Body string }
type Result struct {
	Success   bool
	Uncertain bool
	MessageID string
	Error     error
}
type Sender interface {
	Send(context.Context, Message) Result
	Name() string
}

func NewSender(cfg *config.Config) (Sender, error) {
	if cfg == nil || cfg.Options.DryRun || os.Getenv("ERASER_ENABLE_SEND") != "true" {
		return nil, fmt.Errorf("delivery disabled: dry_run must be false and ERASER_ENABLE_SEND=true")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	password, err := config.ReadSecret(cfg.Email.SMTP.PasswordFile)
	if err != nil {
		return nil, err
	}
	if cfg.Email.SMTP.Username != "" && password == "" {
		return nil, fmt.Errorf("SMTP password_file is required")
	}
	allowed := map[string]bool{}
	for id, a := range cfg.Options.ApprovedBrokers {
		if cfg.Approved(id, a.Email) {
			allowed[a.Email] = true
		}
	}
	return &SMTPSender{config: cfg.Email.SMTP, from: cfg.Email.From, password: password, allowed: allowed}, nil
}
func ValidateEmail(s string) error {
	if !config.ValidAddress(s) {
		return fmt.Errorf("invalid email address")
	}
	return nil
}
func validateMessage(m Message) error {
	if !config.ValidAddress(m.From) || !config.ValidAddress(m.To) || strings.ContainsAny(m.Subject, "\r\n\x00") {
		return fmt.Errorf("invalid message headers")
	}
	return nil
}
