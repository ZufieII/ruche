package agent_test

import (
	"context"
	"net/http/httptest"
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
