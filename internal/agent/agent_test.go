package agent_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"ruche/internal/agent"
	"ruche/internal/protocol"
	"ruche/internal/server"
)

// End to end: dashboard API, agent reports, flight sheet and commands.
func TestAgentAgainstServer(t *testing.T) {
	store, err := server.OpenStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ts := httptest.NewServer((&server.Server{Store: store, AdminPassword: "secret123"}).Handler())
	defer ts.Close()

	rig, token, err := store.CreateRig("rig-1")
	if err != nil {
		t.Fatal(err)
	}
	miner := &agent.FakeMiner{GPUs: 2}
	cpuMiner := &agent.FakeMiner{}
	rebooted := false
	a := &agent.Agent{
		Cfg:    agent.Config{Server: ts.URL, Token: token, Interval: time.Second},
		CPU:    agent.FakeCPU{},
		GPUs:   agent.FakeGPUs{Count: 2},
		Miners: map[string]agent.Miner{protocol.MinerLolMiner: miner, protocol.MinerXMRig: cpuMiner},
		Client: ts.Client(),
		Reboot: func() error { rebooted = true; return nil },
	}
	ctx := context.Background()

	// No flight sheet yet: the rig reports but does not mine.
	if err := a.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if miner.Running() {
		t.Fatal("miner should not run without a flight sheet")
	}
	rigs, _ := store.Rigs()
	if !rigs[0].Online || len(rigs[0].Report.GPUs) != 2 {
		t.Fatalf("rig not reported correctly: %+v", rigs[0])
	}

	fs, err := store.SaveFlightSheet(protocol.FlightSheet{Name: "etc", Miner: "lolminer", Algo: "ETCHASH", Pool: "pool:1010", Wallet: "0xabc"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AssignFlightSheet(rig.ID, &fs.ID); err != nil {
		t.Fatal(err)
	}
	a.Tick(ctx)
	if !miner.Running() {
		t.Fatal("miner should start once a flight sheet is assigned")
	}
	a.Tick(ctx) // next report carries the hashrate
	rigs, _ = store.Rigs()
	if rigs[0].Report.TotalHashrateHs <= 0 || rigs[0].Report.FlightSheetID != fs.ID {
		t.Fatalf("hashrate or flight sheet missing: %+v", rigs[0].Report)
	}

	// Stop sticks until a restart is asked.
	store.QueueCommand(rig.ID, protocol.CmdStopMiner)
	a.Tick(ctx)
	a.Tick(ctx)
	if miner.Running() {
		t.Fatal("miner should stay stopped")
	}
	store.QueueCommand(rig.ID, protocol.CmdRestartMiner)
	a.Tick(ctx)
	if !miner.Running() {
		t.Fatal("miner should run again after restart")
	}

	store.QueueCommand(rig.ID, protocol.CmdReboot)
	a.Tick(ctx)
	if !rebooted {
		t.Fatal("reboot command not applied")
	}

	// Switching to a CPU flight sheet swaps miners.
	cpuFS, _ := store.SaveFlightSheet(protocol.FlightSheet{Name: "xmr", Miner: protocol.MinerXMRig, Algo: "rx/0", Pool: "pool:3333", Wallet: "4abc"})
	store.AssignFlightSheet(rig.ID, &cpuFS.ID)
	a.Tick(ctx)
	if miner.Running() || !cpuMiner.Running() {
		t.Fatal("CPU flight sheet should stop the GPU miner and start the CPU one")
	}
	a.Tick(ctx)
	rigs, _ = store.Rigs()
	if c := rigs[0].Report.CPU; c == nil || c.HashrateHs <= 0 || c.MiningThreads == 0 || rigs[0].Report.Algo != "rx/0" {
		t.Fatalf("CPU stats missing: %+v", rigs[0].Report)
	}

	// Unassigning the flight sheet stops mining.
	store.AssignFlightSheet(rig.ID, nil)
	a.Tick(ctx)
	if cpuMiner.Running() {
		t.Fatal("miner should stop when the flight sheet is removed")
	}

	hist, _ := store.History(rig.ID, time.Now().Add(-time.Hour))
	if len(hist) == 0 {
		t.Fatal("no history recorded")
	}
}

func TestBadTokenRejected(t *testing.T) {
	store, _ := server.OpenStore(filepath.Join(t.TempDir(), "test.db"))
	defer store.Close()
	ts := httptest.NewServer((&server.Server{Store: store, AdminPassword: "secret123"}).Handler())
	defer ts.Close()
	a := &agent.Agent{Cfg: agent.Config{Server: ts.URL, Token: "nope"}, GPUs: agent.FakeGPUs{}}
	if err := a.Tick(context.Background()); err == nil {
		t.Fatal("expected unauthorized")
	}
}

// A miner started by something else (Hive OS) is watched, not controlled.
func TestWatchExternalXMRig(t *testing.T) {
	store, _ := server.OpenStore(filepath.Join(t.TempDir(), "test.db"))
	defer store.Close()
	ts := httptest.NewServer((&server.Server{Store: store, AdminPassword: "secret123"}).Handler())
	defer ts.Close()
	hive := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/2/summary" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(`{"algo":"rx/0","connection":{"pool":"pool.supportxmr.com:3333"},
			"hashrate":{"total":[19800.5,19750,null],"threads":[[1],[1],[1]]},"hugepages":[1168,1168]}`))
	}))
	defer hive.Close()

	_, token, _ := store.CreateRig("rig-02")
	cpuMiner := &agent.FakeMiner{}
	a := &agent.Agent{
		Cfg:    agent.Config{Server: ts.URL, Token: token, WatchXMRig: []string{"http://127.0.0.1:1", hive.URL}},
		CPU:    agent.FakeCPU{},
		Miners: map[string]agent.Miner{protocol.MinerXMRig: cpuMiner},
		Client: ts.Client(),
	}
	if err := a.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	rigs, _ := store.Rigs()
	rep := rigs[0].Report
	if !rep.External || !rep.MinerRunning || rep.TotalHashrateHs != 19800.5 || rep.Algo != "rx/0" ||
		rep.Pool != "pool.supportxmr.com:3333" || rep.CPU.MiningThreads != 3 || rep.CPU.HugePagesPct != 100 {
		t.Fatalf("external miner not reported: %+v", rep)
	}
	if cpuMiner.Running() {
		t.Fatal("watching must not start a miner")
	}
}

func TestConfigWatchXMRig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rig.conf")
	os.WriteFile(path, []byte("server=http://s:8080\ntoken=abc\nwatch_xmrig=http://127.0.0.1:60050, http://127.0.0.1:60051\n"), 0o600)
	cfg, err := agent.LoadConfig(path)
	if err != nil || len(cfg.WatchXMRig) != 2 || cfg.WatchXMRig[1] != "http://127.0.0.1:60051" {
		t.Fatalf("got %+v %v", cfg, err)
	}
}
