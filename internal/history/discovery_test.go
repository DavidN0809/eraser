package history

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/eraser-privacy/eraser/internal/discovery"
)

func TestEvidenceLifecycleAndPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	plan := discovery.Plan{BrokerID: "fixture", Fingerprint: "synthetic-profile"}
	save := func() {
		t.Helper()
		if err := s.SaveScan(plan, []discovery.Match{{URL: "https://broker.example.invalid/person", Title: "Synthetic"}}); err != nil {
			t.Fatal(err)
		}
	}
	save()
	rows, err := s.Matches()
	if err != nil || len(rows) != 1 {
		t.Fatal(err)
	}
	if ok, _ := s.HasConfirmed(plan); ok {
		t.Fatal("pending candidate allowed")
	}
	if err = s.ReviewMatch(rows[0].ID, "different-profile", "confirmed"); err == nil {
		t.Fatal("different profile allowed")
	}
	if err = s.ReviewMatch(rows[0].ID, plan.Fingerprint, "confirmed"); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if ok, err := s.HasConfirmed(plan); !ok || err != nil {
		t.Fatal("confirmation lost after restart", err)
	}
	if err = s.ReviewMatch(rows[0].ID, plan.Fingerprint, "rejected"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.HasConfirmed(plan); ok {
		t.Fatal("rejection did not revoke")
	}
	save()
	if err = s.ReviewMatch(rows[0].ID, plan.Fingerprint, "confirmed"); err == nil {
		t.Fatal("rescan kept stale match")
	}
	rows, _ = s.Matches()
	if err = s.ReviewMatch(rows[0].ID, plan.Fingerprint, "confirmed"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE discovery_scans SET created_at=?`, time.Now().Add(-discovery.MaxAge-time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.HasConfirmed(plan); ok {
		t.Fatal("expired match allowed")
	}
	if err = s.PurgeExpiredDiscovery(); err != nil {
		t.Fatal(err)
	}
	rows, _ = s.Matches()
	if len(rows) != 0 {
		t.Fatal("expired PII retained")
	}
	save()
	if err = s.ForgetDiscovery(plan.BrokerID); err != nil {
		t.Fatal(err)
	}
	rows, _ = s.Matches()
	scans, _ := s.Scans()
	if len(rows) != 0 || len(scans) != 0 {
		t.Fatal("evidence not removed")
	}
	if err = s.SaveScan(plan, nil); err != nil {
		t.Fatal(err)
	}
	scans, _ = s.Scans()
	if len(scans) != 1 || scans[0].Candidates != 0 {
		t.Fatal("zero-result scan lost")
	}
}
