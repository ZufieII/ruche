package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand/v2"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
	// LogTail returns the last n lines the miner printed.
	LogTail(n int) []string
}

// process runs one miner binary and stops it cleanly.
type process struct {
	Binary string
	LogDir string
	// SelfLogging miners write LogPath themselves; their console output is dropped.
	SelfLogging bool

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
	if path := p.LogPath(); path != "" {
		if p.SelfLogging {
			_ = os.Remove(path) // the miner appends; start each run fresh
		} else if f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644); err == nil {
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

// LogPath is where the miner's output goes, or "" when not logging.
func (p *process) LogPath() string {
	if p.LogDir == "" {
		return ""
	}
	name := strings.TrimSuffix(filepath.Base(p.Binary), filepath.Ext(p.Binary))
	return filepath.Join(p.LogDir, name+".log")
}

func (p *process) LogTail(n int) []string { return tailLines(p.LogPath(), n) }

// tailLines reads the last n lines of a file, looking at its last 16 KiB only.
func tailLines(path string, n int) []string {
	if path == "" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	const window = 16 << 10
	if st, err := f.Stat(); err == nil && st.Size() > window {
		f.Seek(st.Size()-window, io.SeekStart)
	}
	data, _ := io.ReadAll(f)
	lines := strings.Split(strings.TrimRight(ansi.ReplaceAllString(string(data), ""), "\r\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], "\r")
	}
	if len(lines) == 1 && lines[0] == "" {
		return nil
	}
	return lines
}

// ansi matches terminal color codes, which some miners print even into files.
var ansi = regexp.MustCompile("\x1b\\[[0-9;]*m")

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
	return &XMRig{process{Binary: binary, LogDir: logDir, SelfLogging: true}}
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

func (m *XMRig) Start(fs protocol.FlightSheet) error {
	args := xmrigArgs(fs)
	// XMRig prints nothing when its output is not a terminal, so let it write its own log.
	if path := m.LogPath(); path != "" {
		args = append(args, "--log-file", path)
	}
	return m.start(args)
}

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

func (f *FakeMiner) LogTail(n int) []string {
	if !f.Running() {
		return nil
	}
	now := time.Now().Format("2006-01-02 15:04:05")
	return []string{
		"[" + now + "] net      use pool pool.example.com:3333",
		"[" + now + "] cpu      accepted (42/0) diff 120001 (31 ms)",
		"[" + now + "] miner    speed 10s/60s/15m 6123.4 6100.2 n/a H/s max 6200.0 H/s",
	}
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
