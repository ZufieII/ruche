package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand/v2"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"ruche/internal/protocol"
)

// Miner runs a mining program for a flight sheet.
type Miner interface {
	Start(fs protocol.FlightSheet) error
	Stop()
	Running() bool
	// Hashrates returns the total and per-GPU hashrate in MH/s.
	Hashrates(ctx context.Context) (float64, []float64)
}

const lolMinerAPIPort = 4068

// LolMiner drives a lolMiner binary and reads its HTTP API.
type LolMiner struct {
	Binary string
	LogDir string

	mu   sync.Mutex
	cmd  *exec.Cmd
	done chan struct{}
}

func lolMinerArgs(fs protocol.FlightSheet) []string {
	user := fs.Wallet
	if fs.Worker != "" {
		user += "." + fs.Worker
	}
	args := []string{"--algo", fs.Algo, "--pool", fs.Pool, "--user", user, "--apiport", fmt.Sprint(lolMinerAPIPort)}
	return append(args, strings.Fields(fs.ExtraArgs)...)
}

func (m *LolMiner) Start(fs protocol.FlightSheet) error {
	m.Stop()
	m.mu.Lock()
	defer m.mu.Unlock()
	cmd := exec.Command(m.Binary, lolMinerArgs(fs)...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if m.LogDir != "" {
		if f, err := os.OpenFile(m.LogDir+"/miner.log", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644); err == nil {
			cmd.Stdout, cmd.Stderr = f, f
		}
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan struct{})
	go func() {
		err := cmd.Wait()
		log.Printf("mineur arrêté: %v", err)
		close(done)
	}()
	m.cmd, m.done = cmd, done
	return nil
}

func (m *LolMiner) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cmd == nil {
		return
	}
	// Signal the whole process group, then force-kill if it hangs.
	_ = syscall.Kill(-m.cmd.Process.Pid, syscall.SIGTERM)
	select {
	case <-m.done:
	case <-time.After(10 * time.Second):
		_ = syscall.Kill(-m.cmd.Process.Pid, syscall.SIGKILL)
		<-m.done
	}
	m.cmd = nil
}

func (m *LolMiner) Running() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cmd == nil {
		return false
	}
	select {
	case <-m.done:
		return false
	default:
		return true
	}
}

func (m *LolMiner) Hashrates(ctx context.Context) (float64, []float64) {
	if !m.Running() {
		return 0, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/", lolMinerAPIPort), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil
	}
	defer resp.Body.Close()
	var body lolMinerSummary
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return 0, nil
	}
	return body.hashrates()
}

// lolMinerSummary is the subset of lolMiner's API (v1.4x and later) we use.
type lolMinerSummary struct {
	Algorithms []struct {
		TotalPerformance  float64   `json:"Total_Performance"`
		PerformanceFactor float64   `json:"Performance_Factor"`
		WorkerPerformance []float64 `json:"Worker_Performance"`
	} `json:"Algorithms"`
}

func (s lolMinerSummary) hashrates() (float64, []float64) {
	if len(s.Algorithms) == 0 {
		return 0, nil
	}
	a := s.Algorithms[0]
	// Performance_Factor converts the reported unit to H/s (1e6 for MH/s).
	toMHs := 1.0
	if a.PerformanceFactor > 0 {
		toMHs = a.PerformanceFactor / 1e6
	}
	per := make([]float64, len(a.WorkerPerformance))
	for i, v := range a.WorkerPerformance {
		per[i] = v * toMHs
	}
	return a.TotalPerformance * toMHs, per
}

// FakeMiner pretends to mine, for demos and tests.
type FakeMiner struct {
	GPUs    int
	running bool
	mu      sync.Mutex
}

func (f *FakeMiner) Start(protocol.FlightSheet) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.running = true
	return nil
}

func (f *FakeMiner) Stop() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.running = false
}

func (f *FakeMiner) Running() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.running
}

func (f *FakeMiner) Hashrates(context.Context) (float64, []float64) {
	if !f.Running() {
		return 0, nil
	}
	per := make([]float64, f.GPUs)
	var total float64
	for i := range per {
		per[i] = 60 + rand.Float64()*2
		total += per[i]
	}
	return total, per
}
