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
	"path/filepath"
	"strings"
	"sync"
	"time"

	"ruche/internal/protocol"
)

// MinerStats is what a running miner reports about its own work.
type MinerStats struct {
	TotalHs  float64
	PerGPUHs []float64
	// CPU-only fields, filled by XMRig.
	CPUThreads   int
	HugePagesPct float64
}

// Miner runs a mining program for a flight sheet.
type Miner interface {
	Start(fs protocol.FlightSheet) error
	Stop()
	Running() bool
	Stats(ctx context.Context) MinerStats
}

// process runs one miner binary and stops it cleanly.
type process struct {
	Binary string
	LogDir string

	mu   sync.Mutex
	cmd  *exec.Cmd
	done chan struct{}
}

func (p *process) start(args []string) error {
	p.Stop()
	p.mu.Lock()
	defer p.mu.Unlock()
	cmd := exec.Command(p.Binary, args...)
	cmd.Dir = filepath.Dir(p.Binary)
	setProcessGroup(cmd)
	if p.LogDir != "" {
		name := strings.TrimSuffix(filepath.Base(p.Binary), filepath.Ext(p.Binary))
		if f, err := os.OpenFile(filepath.Join(p.LogDir, name+".log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644); err == nil {
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
	p.cmd, p.done = cmd, done
	return nil
}

func (p *process) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cmd == nil {
		return
	}
	terminate(p.cmd)
	select {
	case <-p.done:
	case <-time.After(10 * time.Second):
		kill(p.cmd)
		<-p.done
	}
	p.cmd = nil
}

func (p *process) Running() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cmd == nil {
		return false
	}
	select {
	case <-p.done:
		return false
	default:
		return true
	}
}

func getJSON(ctx context.Context, url string, v any) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(v)
}

// --- lolMiner (GPU) ---

const lolMinerAPIPort = 4068

type LolMiner struct{ process }

func NewLolMiner(binary, logDir string) *LolMiner {
	return &LolMiner{process{Binary: binary, LogDir: logDir}}
}

func lolMinerArgs(fs protocol.FlightSheet) []string {
	user := fs.Wallet
	if fs.Worker != "" {
		user += "." + fs.Worker
	}
	args := []string{"--algo", fs.Algo, "--pool", fs.Pool, "--user", user, "--apiport", fmt.Sprint(lolMinerAPIPort)}
	return append(args, strings.Fields(fs.ExtraArgs)...)
}

func (m *LolMiner) Start(fs protocol.FlightSheet) error { return m.start(lolMinerArgs(fs)) }

func (m *LolMiner) Stats(ctx context.Context) MinerStats {
	var body lolMinerSummary
	if !m.Running() || getJSON(ctx, fmt.Sprintf("http://127.0.0.1:%d/", lolMinerAPIPort), &body) != nil {
		return MinerStats{}
	}
	return body.stats()
}

// lolMinerSummary is the subset of lolMiner's API (v1.4x and later) we use.
type lolMinerSummary struct {
	Algorithms []struct {
		TotalPerformance  float64   `json:"Total_Performance"`
		PerformanceFactor float64   `json:"Performance_Factor"`
		WorkerPerformance []float64 `json:"Worker_Performance"`
	} `json:"Algorithms"`
}

func (s lolMinerSummary) stats() MinerStats {
	if len(s.Algorithms) == 0 {
		return MinerStats{}
	}
	a := s.Algorithms[0]
	// Performance_Factor converts the reported unit to H/s (1e6 for MH/s).
	factor := a.PerformanceFactor
	if factor <= 0 {
		factor = 1e6
	}
	per := make([]float64, len(a.WorkerPerformance))
	for i, v := range a.WorkerPerformance {
		per[i] = v * factor
	}
	return MinerStats{TotalHs: a.TotalPerformance * factor, PerGPUHs: per}
}

// --- XMRig (CPU) ---

const xmrigAPIPort = 4067

type XMRig struct{ process }

func NewXMRig(binary, logDir string) *XMRig {
	return &XMRig{process{Binary: binary, LogDir: logDir}}
}

func xmrigArgs(fs protocol.FlightSheet) []string {
	pass := "x"
	if fs.Worker != "" {
		pass = fs.Worker
	}
	args := []string{
		"--url", fs.Pool, "--user", fs.Wallet, "--pass", pass, "--algo", fs.Algo,
		"--http-host", "127.0.0.1", "--http-port", fmt.Sprint(xmrigAPIPort),
		"--no-color", "--donate-level", "1",
	}
	if fs.Worker != "" {
		args = append(args, "--rig-id", fs.Worker)
	}
	return append(args, strings.Fields(fs.ExtraArgs)...)
}

func (m *XMRig) Start(fs protocol.FlightSheet) error { return m.start(xmrigArgs(fs)) }

func (m *XMRig) Stats(ctx context.Context) MinerStats {
	var body xmrigSummary
	if !m.Running() || getJSON(ctx, fmt.Sprintf("http://127.0.0.1:%d/2/summary", xmrigAPIPort), &body) != nil {
		return MinerStats{}
	}
	return body.stats()
}

// xmrigSummary is the subset of XMRig's /2/summary we use.
type xmrigSummary struct {
	Hashrate struct {
		// 10 s, 60 s and 15 min averages; null until enough samples exist.
		Total   []*float64   `json:"total"`
		Threads [][]*float64 `json:"threads"`
	} `json:"hashrate"`
	// [allocated, total] since XMRig 6; older versions sent a bool.
	HugePages json.RawMessage `json:"hugepages"`
}

func firstValue(vals []*float64) float64 {
	for _, v := range vals {
		if v != nil {
			return *v
		}
	}
	return 0
}

func (s xmrigSummary) stats() MinerStats {
	st := MinerStats{TotalHs: firstValue(s.Hashrate.Total), CPUThreads: len(s.Hashrate.Threads)}
	var pages []float64
	var on bool
	switch {
	case json.Unmarshal(s.HugePages, &pages) == nil && len(pages) == 2 && pages[1] > 0:
		st.HugePagesPct = pages[0] * 100 / pages[1]
	case json.Unmarshal(s.HugePages, &on) == nil && on:
		st.HugePagesPct = 100
	}
	return st
}

// --- Simulation ---

// FakeMiner pretends to mine, for demos and tests. With GPUs == 0 it acts as a CPU miner.
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

func (f *FakeMiner) Stats(context.Context) MinerStats {
	if !f.Running() {
		return MinerStats{}
	}
	if f.GPUs == 0 {
		return MinerStats{TotalHs: 6000 + rand.Float64()*300, CPUThreads: 12, HugePagesPct: 100}
	}
	st := MinerStats{PerGPUHs: make([]float64, f.GPUs)}
	for i := range st.PerGPUHs {
		st.PerGPUHs[i] = (60 + rand.Float64()*2) * 1e6
		st.TotalHs += st.PerGPUHs[i]
	}
	return st
}
