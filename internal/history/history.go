package history

import (
	"database/sql"
	"fmt"
	"github.com/eraser-privacy/eraser/internal/config"
	_ "modernc.org/sqlite"
	"os"
	"path/filepath"
	"time"
)

type Record struct {
	ID                                                              int64
	BrokerID, BrokerName, Email, Template, Status, MessageID, Error string
	SentAt                                                          time.Time
}
type Store struct{ db *sql.DB }

func DefaultDBPath() string { return filepath.Join(config.DataDir(), "history.db") }
func NewStore(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	if info, err := os.Lstat(path); err == nil && (!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
		return nil, fmt.Errorf("history must be a regular file")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600) // #nosec G304 -- Private state path is fixed by the local operator, never a request.
	if err != nil {
		return nil, err
	}
	if err = f.Chmod(0600); err != nil {
		_ = f.Close()
		return nil, err
	}
	_ = f.Close()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	// Separate table: upgrading does not alter or expose legacy raw inbox records.
	_, err = db.Exec(`PRAGMA busy_timeout=5000; PRAGMA secure_delete=ON; CREATE TABLE IF NOT EXISTS delivery_history (id INTEGER PRIMARY KEY, broker_id TEXT NOT NULL, status TEXT NOT NULL, created_at TEXT NOT NULL)`)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db}, nil
}
func (s *Store) Close() error { return s.db.Close() }
func (s *Store) Add(r *Record) error {
	_, err := s.db.Exec(`INSERT INTO delivery_history(broker_id,status,created_at) VALUES(?,?,?)`, r.BrokerID, r.Status, time.Now().UTC().Format(time.RFC3339))
	return err
}
func (s *Store) Recent(limit int) ([]Record, error) {
	if limit < 1 || limit > 1000 {
		limit = 100
	}
	rows, err := s.db.Query(`SELECT id,broker_id,status,created_at FROM delivery_history ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Record{}
	for rows.Next() {
		var r Record
		var at string
		if err = rows.Scan(&r.ID, &r.BrokerID, &r.Status, &at); err != nil {
			return nil, err
		}
		r.SentAt, _ = time.Parse(time.RFC3339, at)
		result = append(result, r)
	}
	return result, rows.Err()
}
