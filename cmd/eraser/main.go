package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/eraser-privacy/eraser/internal/broker"
	"github.com/eraser-privacy/eraser/internal/config"
	"github.com/eraser-privacy/eraser/internal/history"
	"github.com/eraser-privacy/eraser/internal/service"
	templates "github.com/eraser-privacy/eraser/internal/template"
	"github.com/eraser-privacy/eraser/internal/web"
	"github.com/spf13/cobra"
)

var version = "dev"

func main() {
	syscall.Umask(0077)
	if err := command().Execute(); err != nil {
		os.Exit(1)
	}
}
func command() *cobra.Command {
	var cfgPath, brokerPath string
	root := &cobra.Command{Use: "eraser", Short: "Private, explicitly approved broker removal requests", SilenceUsage: true}
	root.PersistentFlags().StringVar(&cfgPath, "config", config.DefaultConfigPath(), "Private configuration file")
	defaultBrokers := os.Getenv("ERASER_BROKERS_FILE")
	if defaultBrokers == "" {
		defaultBrokers = "data/brokers.yaml"
	}
	root.PersistentFlags().StringVar(&brokerPath, "brokers", defaultBrokers, "Reviewed broker catalog")
	load := func() (*service.Service, error) {
		cfg, err := config.Load(cfgPath)
		if errors.Is(err, os.ErrNotExist) {
			cfg = &config.Config{Options: config.Options{DryRun: true, Template: "generic", RateLimitMs: 2000}}
			err = nil
		}
		if err != nil {
			return nil, err
		}
		db, err := broker.LoadFromFile(brokerPath)
		if err != nil {
			return nil, err
		}
		engine, err := templates.NewEngine()
		if err != nil {
			return nil, err
		}
		store, err := history.NewStore(history.DefaultDBPath())
		if err != nil {
			return nil, err
		}
		return &service.Service{Config: cfg, Brokers: db, Engine: engine, History: store}, nil
	}
	addDiscoveryCommands(root, load)
	var addr string
	serve := &cobra.Command{Use: "serve", RunE: func(cmd *cobra.Command, args []string) error {
		svc, err := load()
		if err != nil {
			return err
		}
		defer svc.History.Close()
		server, err := web.New(svc)
		if err != nil {
			return err
		}
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		go func() {
			<-ctx.Done()
			shutdown, cancel := context.WithTimeout(context.Background(), 35*time.Second)
			defer cancel()
			_ = server.Shutdown(shutdown)
		}()
		fmt.Println("Eraser ready; authentication required. No work is resumed on startup.")
		return server.Start(addr)
	}}
	serve.Flags().StringVar(&addr, "listen", "127.0.0.1:8080", "Listen address; keep public access behind HTTPS")
	root.AddCommand(serve)
	var id, approval string
	var dry bool
	send := &cobra.Command{Use: "send", Short: "Preview one approved request; live sends require its content digest", RunE: func(cmd *cobra.Command, args []string) error {
		svc, err := load()
		if err != nil {
			return err
		}
		defer svc.History.Close()
		msg, err := svc.Preview(id)
		if err != nil {
			return err
		}
		sum := sha256.Sum256([]byte(msg.From + "\x00" + msg.To + "\x00" + msg.Subject + "\x00" + msg.Body))
		digest := hex.EncodeToString(sum[:])
		if dry || approval == "" || svc.Config.Options.DryRun {
			fmt.Fprintf(cmd.OutOrStdout(), "PREVIEW ONLY\nFrom: %s\nTo: %s\nSubject: %s\n\n%s\nApproval digest: %s\n", msg.From, msg.To, msg.Subject, msg.Body, digest)
			return nil
		}
		if approval != digest {
			return fmt.Errorf("approval digest does not match current request; preview again")
		}
		result, err := svc.Send(cmd.Context(), id)
		if err != nil {
			return err
		}
		if !result.Success {
			return result.Error
		}
		fmt.Fprintln(cmd.OutOrStdout(), "Sent")
		return nil
	}}
	send.Flags().StringVar(&id, "broker", "", "One explicitly approved broker ID")
	send.Flags().BoolVar(&dry, "dry-run", false, "Preview only; never construct a transport")
	send.Flags().StringVar(&approval, "approve-sha256", "", "Digest of the reviewed preview for a live send")
	root.AddCommand(send)
	root.AddCommand(&cobra.Command{Use: "list-brokers", RunE: func(cmd *cobra.Command, args []string) error {
		db, err := broker.LoadFromFile(brokerPath)
		if err != nil {
			return err
		}
		for _, b := range db.Brokers {
			fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\n", b.ID, b.Name, b.Email)
		}
		return nil
	}})
	root.AddCommand(&cobra.Command{Use: "status", RunE: func(cmd *cobra.Command, args []string) error {
		store, err := history.NewStore(history.DefaultDBPath())
		if err != nil {
			return err
		}
		defer store.Close()
		rows, err := store.Recent(100)
		if err != nil {
			return err
		}
		for _, r := range rows {
			fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\n", r.SentAt.Format(time.RFC3339), r.BrokerID, r.Status)
		}
		return nil
	}})
	root.AddCommand(&cobra.Command{Use: "auth-token", Short: "Print the generated web password to the local operator", RunE: func(cmd *cobra.Command, args []string) error {
		token, err := config.ReadSecret(web.TokenPath())
		if err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), token)
		return nil
	}})
	root.AddCommand(&cobra.Command{Use: "version", Run: func(cmd *cobra.Command, args []string) { fmt.Fprintln(cmd.OutOrStdout(), version) }})
	root.AddCommand(&cobra.Command{Use: "healthcheck", RunE: func(cmd *cobra.Command, args []string) error {
		client := http.Client{Timeout: 3 * time.Second}
		resp, err := client.Get("http://127.0.0.1:8080/healthz")
		if err != nil {
			return fmt.Errorf("healthcheck failed")
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return fmt.Errorf("healthcheck failed")
		}
		return nil
	}})
	root.SetVersionTemplate(strings.TrimSpace("{{.Version}}") + "\n")
	return root
}
