package email

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"github.com/eraser-privacy/eraser/internal/config"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestDryRunAndDefaultDenyBeforeDial(t *testing.T) {
	for _, enabled := range []string{"", "true"} {
		t.Setenv("ERASER_ENABLE_SEND", enabled)
		for _, dry := range []bool{true, false} {
			cfg := &config.Config{Options: config.Options{DryRun: dry}}
			if dry || enabled != "true" {
				if _, err := NewSender(cfg); err == nil {
					t.Fatal("transport authorized without both gates")
				}
			}
		}
	}
}
func TestTransportRejectsUnapprovedAndHeadersBeforeDial(t *testing.T) {
	t.Setenv("ERASER_ENABLE_SEND", "true")
	s := &SMTPSender{from: "sender@example.invalid", allowed: map[string]bool{"broker@example.invalid": true}}
	for _, m := range []Message{{To: "other@example.invalid", From: s.from}, {To: "broker@example.invalid", From: s.from, Subject: "hello\r\nBcc: attacker@example.invalid"}, {To: "broker@example.invalid", From: "bad\r\n@example.invalid"}} {
		if s.Send(context.Background(), m).Error == nil {
			t.Fatal("unsafe message accepted")
		}
	}
}
func TestMandatorySTARTTLSBeforeMail(t *testing.T) {
	t.Setenv("ERASER_ENABLE_SEND", "true")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	done := make(chan string, 1)
	go func() {
		c, e := ln.Accept()
		if e != nil {
			done <- ""
			return
		}
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(2 * time.Second))
		_, _ = c.Write([]byte("220 local fixture\r\n"))
		buf := make([]byte, 1024)
		_, _ = c.Read(buf)
		_, _ = c.Write([]byte("250 local fixture\r\n"))
		n, _ := c.Read(buf)
		done <- string(buf[:n])
	}()
	_, p, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(p)
	s := &SMTPSender{config: config.SMTPConfig{Host: "127.0.0.1", Port: port, TLSMode: "starttls"}, from: "sender@example.invalid", allowed: map[string]bool{"broker@example.invalid": true}}
	if s.Send(context.Background(), Message{To: "broker@example.invalid", From: s.from}).Error == nil {
		t.Fatal("plaintext SMTP accepted")
	}
	if extra := <-done; extra != "" {
		t.Fatalf("unexpected SMTP after missing STARTTLS: %q", extra)
	}
}

// All TLS delivery fixtures bind loopback and use synthetic .invalid addresses.
func TestVerifiedTLSDeliveryAndUncertainAcceptance(t *testing.T) {
	for _, mode := range []string{"accepted", "drop-quit", "drop-ack", "untrusted-cert"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("ERASER_ENABLE_SEND", "true")
			fixture := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
			certificate := fixture.TLS.Certificates[0]
			leaf := fixture.Certificate()
			fixture.Close()
			ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12})
			if err != nil {
				t.Fatal(err)
			}
			defer ln.Close()
			received := make(chan string, 1)
			go func() {
				c, e := ln.Accept()
				if e != nil {
					received <- ""
					return
				}
				defer c.Close()
				_ = c.SetDeadline(time.Now().Add(3 * time.Second))
				reader := bufio.NewReader(c)
				write := func(s string) { _, _ = io.WriteString(c, s+"\r\n") }
				write("220 fixture")
				for {
					line, e := reader.ReadString('\n')
					if e != nil {
						received <- ""
						return
					}
					switch {
					case strings.HasPrefix(line, "EHLO"):
						write("250-fixture")
						write("250 AUTH PLAIN")
					case strings.HasPrefix(line, "AUTH PLAIN"):
						write("235 authenticated")
					case strings.HasPrefix(line, "MAIL FROM:"), strings.HasPrefix(line, "RCPT TO:"):
						write("250 OK")
					case strings.HasPrefix(line, "DATA"):
						write("354 continue")
						var body strings.Builder
						for {
							l, e := reader.ReadString('\n')
							if e != nil {
								received <- body.String()
								return
							}
							if l == ".\r\n" {
								break
							}
							body.WriteString(l)
						}
						received <- body.String()
						if mode == "drop-ack" {
							return
						}
						write("250 accepted")
						_, _ = reader.ReadString('\n')
						if mode != "drop-quit" {
							write("221 bye")
						}
						return
					default:
						write("500 unexpected")
					}
				}
			}()
			_, portText, _ := net.SplitHostPort(ln.Addr().String())
			port, _ := strconv.Atoi(portText)
			secret := filepath.Join(t.TempDir(), "smtp")
			if err = os.WriteFile(secret, []byte("synthetic-password"), 0600); err != nil {
				t.Fatal(err)
			}
			cfg := &config.Config{Email: config.EmailConfig{Provider: "smtp", From: "sender@example.invalid", SMTP: config.SMTPConfig{Host: "127.0.0.1", Port: port, Username: "fixture", PasswordFile: secret, TLSMode: "implicit"}}, Options: config.Options{ApprovedBrokers: map[string]config.Approval{"test": {Email: "broker@example.invalid"}}}}
			sender, err := NewSender(cfg)
			if err != nil {
				t.Fatal(err)
			}
			s := sender.(*SMTPSender)
			if mode != "untrusted-cert" {
				s.rootCAs = x509.NewCertPool()
				s.rootCAs.AddCert(leaf)
			}
			result := s.Send(context.Background(), Message{From: cfg.Email.From, To: "broker@example.invalid", Subject: "Synthetic request", Body: "Approved field only"})
			body := <-received
			switch mode {
			case "accepted", "drop-quit":
				if !result.Success || result.Uncertain {
					t.Fatalf("accepted message misclassified: %+v", result)
				}
				if !strings.Contains(body, "Approved field only") || strings.Contains(body, "synthetic-password") {
					t.Fatal("incorrect payload")
				}
			case "drop-ack":
				if result.Success || !result.Uncertain {
					t.Fatal("lost DATA acknowledgement must be uncertain")
				}
			case "untrusted-cert":
				if result.Success || body != "" {
					t.Fatal("untrusted TLS sent payload")
				}
			}
		})
	}
}
