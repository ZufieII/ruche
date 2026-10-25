package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ruche/internal/protocol"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestAdminAuth(t *testing.T) {
	ts := httptest.NewServer((&Server{Store: newStore(t), AdminPassword: "secret123"}).Handler())
	defer ts.Close()

	resp, _ := http.Get(ts.URL + "/api/rigs")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", resp.StatusCode)
	}
	req, _ := http.NewRequest("POST", ts.URL+"/api/rigs", strings.NewReader(`{"name":"rig-a"}`))
	req.SetBasicAuth("admin", "secret123")
	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
}

type recorder struct{ msgs []string }

func (r *recorder) Notify(_ context.Context, m string) error {
	r.msgs = append(r.msgs, m)
	return nil
}

func TestAlerts(t *testing.T) {
	s := newStore(t)
	rig, _, _ := s.CreateRig("rig-a")
	rec := &recorder{}
	a := &Alerter{Store: s, Notifier: rec, MaxTempC: 80}

	s.SaveReport(rig.ID, protocol.Report{GPUs: []protocol.GPUStat{{TempC: 85}}})
	a.Check(context.Background())
	a.Check(context.Background()) // no duplicate
	if len(rec.msgs) != 1 || !strings.Contains(rec.msgs[0], "85") {
		t.Fatalf("want one heat alert, got %q", rec.msgs)
	}

	s.OfflineAfter = 0 // everything is now stale
	time.Sleep(10 * time.Millisecond)
	a.Check(context.Background())
	if len(rec.msgs) != 2 || !strings.Contains(rec.msgs[1], "hors ligne") {
		t.Fatalf("want offline alert, got %q", rec.msgs)
	}

	s.OfflineAfter = time.Minute
	a.Check(context.Background())
	if len(rec.msgs) != 4 || !strings.Contains(rec.msgs[2], "retour") {
		t.Fatalf("want recovery (and heat again), got %q", rec.msgs)
	}
}

func TestBadCommandRejected(t *testing.T) {
	s := newStore(t)
	rig, _, _ := s.CreateRig("rig-a")
	if err := s.QueueCommand(rig.ID, "rm -rf /"); err == nil {
		t.Fatal("unknown command accepted")
	}
}
