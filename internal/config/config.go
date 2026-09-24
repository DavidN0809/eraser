package config

import (
	"bytes"
	"fmt"
	"io"
	"net/mail"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Profile Profile     `yaml:"profile"`
	Email   EmailConfig `yaml:"email"`
	Options Options     `yaml:"options"`
}
type Profile struct {
	FirstName   string `yaml:"first_name"`
	LastName    string `yaml:"last_name"`
	Email       string `yaml:"email"`
	Address     string `yaml:"address,omitempty"`
	City        string `yaml:"city,omitempty"`
	State       string `yaml:"state,omitempty"`
	ZipCode     string `yaml:"zip_code,omitempty"`
	Country     string `yaml:"country,omitempty"`
	Phone       string `yaml:"phone,omitempty"`
	DateOfBirth string `yaml:"date_of_birth,omitempty"`
}

func (p Profile) FullName() string { return strings.TrimSpace(p.FirstName + " " + p.LastName) }

type EmailConfig struct {
	Provider string     `yaml:"provider"`
	From     string     `yaml:"from"`
	SMTP     SMTPConfig `yaml:"smtp"`
}
type SMTPConfig struct {
	Host         string `yaml:"host"`
	Port         int    `yaml:"port"`
	Username     string `yaml:"username"`
	PasswordFile string `yaml:"password_file"`
	TLSMode      string `yaml:"tls_mode"` // implicit or starttls; plaintext is unsupported
}
type Approval struct {
	Email  string   `yaml:"email"`  // Bind approval to both broker ID and exact recipient.
	Fields []string `yaml:"fields"` // Optional profile fields explicitly approved for this broker.
}
type Options struct {
	Template        string              `yaml:"template"`
	DryRun          bool                `yaml:"dry_run"`
	RateLimitMs     int                 `yaml:"rate_limit_ms"`
	Regions         []string            `yaml:"regions"`
	ExcludedBrokers []string            `yaml:"excluded_brokers"`
	ApprovedBrokers map[string]Approval `yaml:"approved_brokers"`
}

func DataDir() string {
	if p := os.Getenv("ERASER_DATA_DIR"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".eraser"
	}
	return filepath.Join(home, ".eraser")
}
func DefaultConfigPath() string {
	if p := os.Getenv("ERASER_CONFIG_FILE"); p != "" {
		return p
	}
	return filepath.Join(DataDir(), "config.yaml")
}
func Load(path string) (*Config, error) {
	// A mounted Secret may be group-readable by the non-root service group, never world-readable/writable.
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("read configuration: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0027 != 0 {
		return nil, fmt.Errorf("configuration must be a regular file with mode 0600 or 0640")
	}
	f, err := os.Open(path) // #nosec G304 -- Path comes only from trusted operator configuration/secret mounts.
	if err != nil {
		return nil, err
	}
	defer f.Close()
	cfg := Config{Options: Options{DryRun: true, Template: "generic", RateLimitMs: 2000}}
	d := yaml.NewDecoder(io.LimitReader(f, 1<<20))
	d.KnownFields(true)
	if err = d.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("invalid configuration schema (legacy credential/pipeline fields are unsupported)")
	}
	var extra any
	if err = d.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("configuration must contain one YAML document")
	}
	if cfg.Options.RateLimitMs < 1000 {
		cfg.Options.RateLimitMs = 1000
	}
	if cfg.Options.RateLimitMs > 60000 {
		return nil, fmt.Errorf("rate_limit_ms must be <= 60000")
	}
	if cfg.Email.Provider != "" && cfg.Email.Provider != "smtp" {
		return nil, fmt.Errorf("only SMTP is supported")
	}
	if cfg.Options.Template != "generic" && cfg.Options.Template != "gdpr" && cfg.Options.Template != "ccpa" {
		return nil, fmt.Errorf("unknown template")
	}
	for _, a := range cfg.Options.ApprovedBrokers {
		if !ValidAddress(a.Email) {
			return nil, fmt.Errorf("approved recipient must be a bare email address")
		}
		for _, field := range a.Fields {
			switch field {
			case "name", "email", "address", "city", "state", "zip_code", "country", "phone", "date_of_birth":
			default:
				return nil, fmt.Errorf("unknown disclosure field")
			}
		}
	}
	return &cfg, nil
}
func ValidAddress(s string) bool {
	if strings.ContainsAny(s, "\r\n,;\x00") {
		return false
	}
	a, e := mail.ParseAddress(s)
	return e == nil && a.Address == s
}
func (c *Config) Validate() error {
	if !ValidAddress(c.Email.From) {
		return fmt.Errorf("email.from must be a bare email address")
	}
	if c.Email.SMTP.Host == "" || strings.ContainsAny(c.Email.SMTP.Host, "/\r\n ") {
		return fmt.Errorf("SMTP host required")
	}
	if c.Email.SMTP.Port < 1 || c.Email.SMTP.Port > 65535 {
		return fmt.Errorf("invalid SMTP port")
	}
	if c.Email.SMTP.TLSMode != "implicit" && c.Email.SMTP.TLSMode != "starttls" {
		return fmt.Errorf("SMTP tls_mode must be implicit or starttls")
	}
	return nil
}
func (c *Config) Approved(id, recipient string) bool {
	for _, excluded := range c.Options.ExcludedBrokers {
		if strings.EqualFold(excluded, id) {
			return false
		}
	}
	a, ok := c.Options.ApprovedBrokers[id]
	return ok && a.Email == recipient
}
func (c *Config) Disclosure(id string) Profile {
	p := Profile{}
	for _, f := range c.Options.ApprovedBrokers[id].Fields {
		switch f {
		case "name":
			p.FirstName = c.Profile.FirstName
			p.LastName = c.Profile.LastName
		case "email":
			p.Email = c.Profile.Email
		case "address":
			p.Address = c.Profile.Address
		case "city":
			p.City = c.Profile.City
		case "state":
			p.State = c.Profile.State
		case "zip_code":
			p.ZipCode = c.Profile.ZipCode
		case "country":
			p.Country = c.Profile.Country
		case "phone":
			p.Phone = c.Profile.Phone
		case "date_of_birth":
			p.DateOfBirth = c.Profile.DateOfBirth
		}
	}
	return p
}
func ReadSecret(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	f, err := os.Open(path) // #nosec G304 -- Path comes only from trusted operator configuration/secret mounts.
	if err != nil {
		return "", fmt.Errorf("cannot read secret file")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0027 != 0 {
		return "", fmt.Errorf("secret must be regular and mode 0600 or 0640")
	}
	b, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil || len(b) > 4096 {
		return "", fmt.Errorf("invalid secret file")
	}
	return string(bytes.TrimRight(b, "\r\n")), nil
}
