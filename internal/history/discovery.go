package history

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/eraser-privacy/eraser/internal/discovery"
)

type Scan struct {
	BrokerID, CreatedAt string
	Candidates          int
}

func (s *Store) initDiscovery() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS discovery_scans (broker_id TEXT PRIMARY KEY, fingerprint TEXT NOT NULL, created_at INTEGER NOT NULL);
 CREATE TABLE IF NOT EXISTS discovery_matches (id TEXT PRIMARY KEY, broker_id TEXT NOT NULL, url TEXT NOT NULL, title TEXT NOT NULL, snippet TEXT NOT NULL, status TEXT NOT NULL)`)
	return err
}
func (s *Store) SaveScan(plan discovery.Plan, matches []discovery.Match) error {
	if len(matches) > 20 {
		return fmt.Errorf("too many candidates")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`DELETE FROM discovery_matches WHERE broker_id=?`, plan.BrokerID); err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO discovery_scans(broker_id,fingerprint,created_at) VALUES(?,?,?) ON CONFLICT(broker_id) DO UPDATE SET fingerprint=excluded.fingerprint,created_at=excluded.created_at`, plan.BrokerID, plan.Fingerprint, time.Now().Unix()); err != nil {
		return err
	}
	for _, m := range matches {
		var token [16]byte
		if _, err = rand.Read(token[:]); err != nil {
			return err
		}
		if _, err = tx.Exec(`INSERT INTO discovery_matches(id,broker_id,url,title,snippet,status) VALUES(?,?,?,?,?,'pending')`, hex.EncodeToString(token[:]), plan.BrokerID, m.URL, m.Title, m.Snippet); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *Store) Matches() ([]discovery.Match, error) {
	rows, err := s.db.Query(`SELECT m.id,m.broker_id,m.url,m.title,m.snippet,m.status FROM discovery_matches m JOIN discovery_scans s ON s.broker_id=m.broker_id WHERE s.created_at>=? ORDER BY s.created_at DESC,m.id LIMIT 1000`, time.Now().Add(-discovery.MaxAge).Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []discovery.Match{}
	for rows.Next() {
		var m discovery.Match
		if err = rows.Scan(&m.ID, &m.BrokerID, &m.URL, &m.Title, &m.Snippet, &m.Status); err != nil {
			return nil, err
		}
		result = append(result, m)
	}
	return result, rows.Err()
}
func (s *Store) MatchBroker(id string) (string, error) {
	var broker string
	err := s.db.QueryRow(`SELECT broker_id FROM discovery_matches WHERE id=?`, id).Scan(&broker)
	return broker, err
}
func (s *Store) ReviewMatch(id, fingerprint, status string) error {
	if status != "confirmed" && status != "rejected" {
		return fmt.Errorf("decision must be confirmed or rejected")
	}
	res, err := s.db.Exec(`UPDATE discovery_matches SET status=? WHERE id=? AND broker_id IN (SELECT broker_id FROM discovery_scans WHERE fingerprint=? AND created_at>=?)`, status, id, fingerprint, time.Now().Add(-discovery.MaxAge).Unix())
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("match expired or profile/catalog changed; search again")
	}
	return nil
}
func (s *Store) HasConfirmed(plan discovery.Plan) (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM discovery_matches m JOIN discovery_scans s ON s.broker_id=m.broker_id WHERE m.broker_id=? AND s.fingerprint=? AND s.created_at>=? AND m.status='confirmed'`, plan.BrokerID, plan.Fingerprint, time.Now().Add(-discovery.MaxAge).Unix()).Scan(&n)
	return n > 0, err
}
func (s *Store) ForgetDiscovery(broker string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`DELETE FROM discovery_matches WHERE broker_id=?`, broker); err != nil {
		return err
	}
	if _, err = tx.Exec(`DELETE FROM discovery_scans WHERE broker_id=?`, broker); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) PurgeExpiredDiscovery() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	cutoff := time.Now().Add(-discovery.MaxAge).Unix()
	if _, err = tx.Exec(`DELETE FROM discovery_matches WHERE broker_id IN (SELECT broker_id FROM discovery_scans WHERE created_at<?)`, cutoff); err != nil {
		return err
	}
	if _, err = tx.Exec(`DELETE FROM discovery_scans WHERE created_at<?`, cutoff); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) Scans() ([]Scan, error) {
	rows, err := s.db.Query(`SELECT s.broker_id,s.created_at,COUNT(m.id) FROM discovery_scans s LEFT JOIN discovery_matches m ON s.broker_id=m.broker_id WHERE s.created_at>=? GROUP BY s.broker_id ORDER BY s.created_at DESC LIMIT 1000`, time.Now().Add(-discovery.MaxAge).Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	scans := []Scan{}
	for rows.Next() {
		var s Scan
		var at int64
		if err = rows.Scan(&s.BrokerID, &at, &s.Candidates); err != nil {
			return nil, err
		}
		s.CreatedAt = time.Unix(at, 0).UTC().Format(time.RFC3339)
		scans = append(scans, s)
	}
	return scans, rows.Err()
}
