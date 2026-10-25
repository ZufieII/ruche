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

func TestMarketSource(t *testing.T) {
	calls := 0
	priceUp := true
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch r.URL.Path {
		case "/network":
			w.Write([]byte(`{"difficulty":400000000000,"hash":"ab","height":3300000,"value":600000000000,"ts":1}`))
		case "/price":
			if !priceUp {
				http.Error(w, "rate limited", http.StatusTooManyRequests)
				return
			}
			w.Write([]byte(`{"monero":{"eur":150.5,"usd":170.25}}`))
		}
	}))
	defer api.Close()
	m := &MarketSource{NetworkURL: api.URL + "/network", PriceURL: api.URL + "/price", TTL: time.Hour, Client: api.Client()}

	got := m.Get(context.Background())
	if got.Difficulty != 4e11 || got.RewardXMR != 0.6 || got.PriceEUR != 150.5 || got.Error != "" {
		t.Fatalf("got %+v", got)
	}
	m.Get(context.Background())
	if calls != 2 {
		t.Fatalf("cache not used: %d calls", calls)
	}

	// Price API down: network stats still come back, with an error note.
	m.TTL = 0
	priceUp = false
	got = m.Get(context.Background())
	if got.Difficulty != 4e11 || got.PriceEUR != 0 || got.Error == "" {
		t.Fatalf("got %+v", got)
	}
}

func TestSettingsAndPower(t *testing.T) {
	s := newStore(t)
	if set, err := s.Settings(); err != nil || set.ElectricityPerKWh != 0 {
		t.Fatalf("default settings: %+v %v", set, err)
	}
	s.SaveSettings(Settings{ElectricityPerKWh: 0.2516})
	s.SaveSettings(Settings{ElectricityPerKWh: 0.2276})
	if set, _ := s.Settings(); set.ElectricityPerKWh != 0.2276 {
		t.Fatalf("got %+v", set)
	}
	rig, _, _ := s.CreateRig("rig-a")
	if err := s.SetRigPower(rig.ID, 180); err != nil {
		t.Fatal(err)
	}
	rigs, _ := s.Rigs()
	if rigs[0].PowerW != 180 {
		t.Fatalf("got %+v", rigs[0])
	}
	if err := s.SetRigPower("nope", 1); err != ErrNotFound {
		t.Fatalf("want not found, got %v", err)
	}
}

func TestMigrationKeepsOldDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	s, _ := OpenStore(path)
	s.CreateRig("rig-a")
	s.Close()
	// Reopening runs the ALTER again, which must be harmless.
	s2, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if rigs, _ := s2.Rigs(); len(rigs) != 1 {
		t.Fatal("rig lost after reopening")
	}
}
