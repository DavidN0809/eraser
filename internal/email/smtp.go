package email

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"github.com/eraser-privacy/eraser/internal/config"
	"net"
	"net/smtp"
	"os"
	"strconv"
	"time"
)

var errDeliveryUncertain = errors.New("delivery acceptance uncertain")

type SMTPSender struct {
	rootCAs        *x509.CertPool // nil uses system trust; package-local fixtures supply a test CA.
	config         config.SMTPConfig
	from, password string
	allowed        map[string]bool
}

func (s *SMTPSender) Name() string { return "smtp" }
func (s *SMTPSender) Send(ctx context.Context, msg Message) Result {
	// The transport itself fails closed even if a caller forgets the higher-level gate.
	if os.Getenv("ERASER_ENABLE_SEND") != "true" || !s.allowed[msg.To] || msg.From != s.from {
		return Result{Error: fmt.Errorf("delivery is not authorized")}
	}
	if err := validateMessage(msg); err != nil {
		return Result{Error: err}
	}
	if err := s.deliver(ctx, msg); err != nil {
		if errors.Is(err, errDeliveryUncertain) {
			return Result{Uncertain: true, Error: fmt.Errorf("SMTP acceptance uncertain; inspect provider before retrying")}
		}
		return Result{Error: fmt.Errorf("SMTP delivery failed; check server, TLS and credentials")}
	}
	return Result{Success: true}
}
func (s *SMTPSender) deliver(parent context.Context, msg Message) error {
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	dialer := net.Dialer{Timeout: 10 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(s.config.Host, strconv.Itoa(s.config.Port)))
	if err != nil {
		return err
	}
	defer conn.Close()
	deadline, _ := ctx.Deadline()
	if err = conn.SetDeadline(deadline); err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	tc := &tls.Config{ServerName: s.config.Host, MinVersion: tls.VersionTLS12, RootCAs: s.rootCAs}
	switch s.config.TLSMode {
	case "implicit":
		secured := tls.Client(conn, tc)
		if err = secured.HandshakeContext(ctx); err != nil {
			return err
		}
		conn = secured
	case "starttls":
	default:
		return fmt.Errorf("TLS required")
	}
	c, err := smtp.NewClient(conn, s.config.Host)
	if err != nil {
		return err
	}
	defer c.Close()
	if s.config.TLSMode == "starttls" {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return fmt.Errorf("STARTTLS required")
		}
		if err = c.StartTLS(tc); err != nil {
			return err
		}
	}
	if s.config.Username != "" {
		if err = c.Auth(smtp.PlainAuth("", s.config.Username, s.password, s.config.Host)); err != nil {
			return err
		}
	}
	if err = c.Mail(msg.From); err != nil {
		return err
	}
	if err = c.Rcpt(msg.To); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n%s", msg.From, msg.To, msg.Subject, msg.Body)
	if err != nil {
		return errDeliveryUncertain
	}
	if err = w.Close(); err != nil {
		return errDeliveryUncertain
	}
	// DATA acknowledgement is authoritative; QUIT failure must not encourage a duplicate send.
	_ = c.Quit()
	return nil
}
