package broker

import (
	"fmt"
	"net/mail"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

func isValidURL(rawURL string) bool {
	if rawURL == "" {
		return true
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	scheme := strings.ToLower(u.Scheme)
	return (scheme == "http" || scheme == "https") && u.Hostname() != "" && u.User == nil
}

func sanitizeBroker(b *Broker) {
	if !isValidURL(b.OptOutURL) {
		b.OptOutURL = ""
	}
	if !isValidURL(b.Website) {
		b.Website = ""
	}
}

type Broker struct {
	ID         string   `yaml:"id"`
	Name       string   `yaml:"name"`
	Email      string   `yaml:"email"`
	Website    string   `yaml:"website,omitempty"`
	OptOutURL  string   `yaml:"opt_out_url,omitempty"`
	Region     string   `yaml:"region"`             // "us", "eu", "global"
	Category   string   `yaml:"category,omitempty"` // "people-search", "marketing", "background-check", etc.
	Notes      string   `yaml:"notes,omitempty"`
	RequiresID bool     `yaml:"requires_id,omitempty"` // If they require ID verification
	Tags       []string `yaml:"tags,omitempty"`
}

type BrokerDatabase struct {
	Brokers []Broker `yaml:"brokers"`
}

func LoadFromFile(path string) (*BrokerDatabase, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- Catalog path is local administrator configuration, never HTTP input.
	if err != nil {
		return nil, fmt.Errorf("failed to read broker file: %w", err)
	}

	var db BrokerDatabase
	if err := yaml.Unmarshal(data, &db); err != nil {
		return nil, fmt.Errorf("failed to parse broker file: %w", err)
	}

	seen := map[string]bool{}
	for i := range db.Brokers {
		b := &db.Brokers[i]
		if !regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,99}$`).MatchString(b.ID) || seen[b.ID] {
			return nil, fmt.Errorf("invalid or duplicate broker ID at entry %d", i)
		}
		seen[b.ID] = true
		if b.Email != "" {
			a, e := mail.ParseAddress(b.Email)
			if e != nil || a.Address != b.Email || strings.ContainsAny(b.Email, "\r\n,;\x00") {
				return nil, fmt.Errorf("invalid broker recipient at entry %d", i)
			}
		}
		sanitizeBroker(b)
	}
	return &db, nil
}

func LoadFromDir(dir string) (*BrokerDatabase, error) {
	db := &BrokerDatabase{}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("failed to read broker directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if !strings.HasSuffix(entry.Name(), ".yaml") && !strings.HasSuffix(entry.Name(), ".yml") {
			continue
		}

		path := filepath.Join(dir, entry.Name())
		partialDB, err := LoadFromFile(path)
		if err != nil {
			return nil, fmt.Errorf("failed to load %s: %w", entry.Name(), err)
		}

		db.Brokers = append(db.Brokers, partialDB.Brokers...)
	}

	return db, nil
}

func toSet(items []string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, s := range items {
		m[strings.ToLower(s)] = true
	}
	return m
}

func (db *BrokerDatabase) Filter(regions []string, excluded []string) []Broker {
	regionSet, excludedSet := toSet(regions), toSet(excluded)

	var result []Broker
	for _, b := range db.Brokers {
		if excludedSet[strings.ToLower(b.ID)] || excludedSet[strings.ToLower(b.Name)] {
			continue
		}
		if len(regionSet) > 0 {
			r := strings.ToLower(b.Region)
			if !regionSet[r] && !regionSet["global"] && r != "global" {
				continue
			}
		}
		result = append(result, b)
	}
	return result
}

func (db *BrokerDatabase) FindByID(id string) *Broker {
	id = strings.ToLower(id)
	for i := range db.Brokers {
		if strings.ToLower(db.Brokers[i].ID) == id {
			return &db.Brokers[i]
		}
	}
	return nil
}
