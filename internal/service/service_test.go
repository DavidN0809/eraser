package service

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/pem"
	"github.com/eraser-privacy/eraser/internal/broker"
	"github.com/eraser-privacy/eraser/internal/config"
	"github.com/eraser-privacy/eraser/internal/discovery"
	"github.com/eraser-privacy/eraser/internal/history"
	templates "github.com/eraser-privacy/eraser/internal/template"
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

func TestApprovedDeliveryPersistsMinimizedResult(t *testing.T) {
	t.Setenv("ERASER_ENABLE_SEND", "true")
	fixture := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	cert := fixture.TLS.Certificates[0]
	leaf := fixture.Certificate()
	fixture.Close()
	d := t.TempDir()
	ca := filepath.Join(d, "test-ca.pem")
	if e := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leaf.Raw}), 0600); e != nil {
		t.Fatal(e)
	}
	t.Setenv("SSL_CERT_FILE", ca)
	ln, e := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
	if e != nil {
		t.Fatal(e)
	}
	defer ln.Close()
	received := make(chan string, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			received <- ""
			return
		}
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(3 * time.Second))
		rd := bufio.NewReader(c)
		write := func(line string) { _, _ = io.WriteString(c, line+"\r\n") }
		write("220 fixture")
		for {
			line, err := rd.ReadString('\n')
			if err != nil {
				received <- ""
				return
			}
			switch {
			case strings.HasPrefix(line, "EHLO"):
				write("250 fixture")
			case strings.HasPrefix(line, "MAIL FROM:"), strings.HasPrefix(line, "RCPT TO:"):
				write("250 OK")
			case strings.HasPrefix(line, "DATA"):
				write("354 continue")
				var body strings.Builder
				for {
					v, e := rd.ReadString('\n')
					if e != nil {
						received <- ""
						return
					}
					if v == ".\r\n" {
						break
					}
					body.WriteString(v)
				}
				received <- body.String()
				write("250 accepted")
				_, _ = rd.ReadString('\n')
				write("221 bye")
				return
			default:
				write("500 unexpected")
			}
		}
	}()
	_, portString, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(portString)
	cfg := &config.Config{Discovery: config.DiscoveryConfig{Fields: []string{"name"}}, Profile: config.Profile{FirstName: "Synthetic", LastName: "Person", Phone: "NEVER-DISCLOSE-PHONE", DateOfBirth: "NEVER-DISCLOSE-DOB"}, Email: config.EmailConfig{Provider: "smtp", From: "sender@example.invalid", SMTP: config.SMTPConfig{Host: "127.0.0.1", Port: port, TLSMode: "implicit"}}, Options: config.Options{Template: "generic", RateLimitMs: 2000, ApprovedBrokers: map[string]config.Approval{"fixture": {Email: "broker@example.invalid", Fields: []string{"name"}}}}}
	h, e := history.NewStore(filepath.Join(d, "history.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer h.Close()
	engine, e := templates.NewEngine()
	if e != nil {
		t.Fatal(e)
	}
	svc := &Service{Config: cfg, History: h, Engine: engine, Brokers: &broker.BrokerDatabase{Brokers: []broker.Broker{{ID: "fixture", Website: "https://broker.example.invalid", Email: "broker@example.invalid"}}}}
	// A send must fail before any SMTP connection until a candidate is manually confirmed.
	if _, e = svc.Send(context.Background(), "fixture"); e == nil || !strings.Contains(e.Error(), "confirmed discovery match") {
		t.Fatal("missing evidence allowed", e)
	}
	plan, e := svc.DiscoveryPlan("fixture")
	if e != nil {
		t.Fatal(e)
	}
	if e = h.SaveScan(plan, []discovery.Match{{URL: "https://broker.example.invalid/person"}}); e != nil {
		t.Fatal(e)
	}
	if _, e = svc.Send(context.Background(), "fixture"); e == nil {
		t.Fatal("pending evidence allowed")
	}
	matches, e := h.Matches()
	if e != nil {
		t.Fatal(e)
	}
	if e = svc.ReviewMatch(matches[0].ID, "confirmed"); e != nil {
		t.Fatal(e)
	}
	cfg.Profile.Email = "changed@example.invalid"
	if _, e = svc.Send(context.Background(), "fixture"); e == nil {
		t.Fatal("changed identity allowed")
	}
	cfg.Profile.Email = ""
	result, e := svc.Send(context.Background(), "fixture")
	if e != nil || !result.Success {
		t.Fatalf("legitimate TLS delivery failed: %v %+v", e, result)
	}
	body := <-received
	if !strings.Contains(body, "Synthetic Person") || strings.Contains(body, "NEVER-DISCLOSE") {
		t.Fatal("wrong disclosure")
	}
	rows, e := h.Recent(10)
	if e != nil || len(rows) != 2 || rows[0].Status != "sent" || rows[1].Status != "attempted" {
		t.Fatalf("history mismatch: %+v %v", rows, e)
	}
	if _, e = svc.Send(context.Background(), "fixture"); e == nil {
		t.Fatal("rate limit not enforced")
	}
	info, e := os.Stat(filepath.Join(d, "history.db"))
	if e != nil || info.Mode().Perm() != 0600 {
		t.Fatal("database not private")
	}
}
