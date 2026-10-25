// Package agent runs on each mining rig: it reports stats and applies the server's orders.
package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"ruche/internal/protocol"
)

const Version = "0.3.0"

type Config struct {
	Server   string        // e.g. https://ruche.example.com
	Token    string        // rig token shown when the rig was created
	Interval time.Duration // time between reports
}

// LoadConfig reads a key=value file such as /etc/ruche/rig.conf.
func LoadConfig(path string) (Config, error) {
	cfg := Config{Interval: 10 * time.Second}
	f, err := os.Open(path)
	if err != nil {
		return cfg, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		val = strings.Trim(strings.TrimSpace(val), `"`)
		switch strings.TrimSpace(key) {
		case "server":
			cfg.Server = strings.TrimRight(val, "/")
		case "token":
			cfg.Token = val
		case "interval":
			if d, err := time.ParseDuration(val); err == nil && d >= time.Second {
				cfg.Interval = d
			}
		}
	}
	if cfg.Server == "" || cfg.Token == "" {
		return cfg, fmt.Errorf("%s: server et token sont obligatoires", path)
	}
	return cfg, sc.Err()
}

type Agent struct {
	Cfg    Config
	CPU    CPUReader
	GPUs   GPUReader
	Miners map[string]Miner // by protocol.Miner* name
	Client *http.Client
	// Reboot restarts the machine; replaced in tests.
	Reboot func() error

	started time.Time
	current *protocol.FlightSheet
	stopped bool // the user asked to stop mining
}

func (a *Agent) Run(ctx context.Context) {
	a.started = time.Now()
	if a.Reboot == nil {
		a.Reboot = func() error { return rebootCommand().Run() }
	}
	t := time.NewTicker(a.Cfg.Interval)
	defer t.Stop()
	for {
		if err := a.Tick(ctx); err != nil {
			log.Printf("rapport échoué: %v", err)
		}
		select {
		case <-ctx.Done():
			a.stopMiner()
			return
		case <-t.C:
		}
	}
}

// Tick sends one report and applies the response.
func (a *Agent) Tick(ctx context.Context) error {
	resp, err := a.send(ctx, a.report(ctx))
	if err != nil {
		return err
	}
	a.apply(resp)
	return nil
}

// miner returns the miner of the current flight sheet, or nil.
func (a *Agent) miner() Miner {
	if a.current == nil {
		return nil
	}
	return a.Miners[a.current.Miner]
}

func (a *Agent) stopMiner() {
	if m := a.miner(); m != nil {
		m.Stop()
	}
}

func (a *Agent) report(ctx context.Context) protocol.Report {
	host, _ := os.Hostname()
	rep := protocol.Report{
		Hostname:      host,
		AgentVersion:  Version,
		UptimeS:       int64(time.Since(a.started).Seconds()),
		SystemUptimeS: systemUptime(),
	}
	if a.GPUs != nil {
		rep.GPUs = a.GPUs.Read()
	}
	if a.CPU != nil {
		cpu := a.CPU.Read()
		rep.CPU = &cpu
	}
	m := a.miner()
	if m == nil {
		return rep
	}
	rep.Miner, rep.Algo, rep.FlightSheetID = a.current.Miner, a.current.Algo, a.current.ID
	rep.MinerRunning = m.Running()
	rep.MinerLog = m.LogTail(30)
	st := m.Stats(ctx)
	rep.TotalHashrateHs = st.TotalHs
	for i := range rep.GPUs {
		if i < len(st.PerGPUHs) {
			rep.GPUs[i].HashrateHs = st.PerGPUHs[i]
		}
	}
	if a.current.Miner == protocol.MinerXMRig && rep.CPU != nil {
		rep.CPU.HashrateHs, rep.CPU.MiningThreads, rep.CPU.HugePagesPct = st.TotalHs, st.CPUThreads, st.HugePagesPct
	}
	return rep
}

func (a *Agent) send(ctx context.Context, rep protocol.Report) (protocol.ReportResponse, error) {
	var out protocol.ReportResponse
	body, _ := json.Marshal(rep)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.Cfg.Server+"/api/agent/report", bytes.NewReader(body))
	if err != nil {
		return out, err
	}
	req.Header.Set("Authorization", "Bearer "+a.Cfg.Token)
	req.Header.Set("Content-Type", "application/json")
	if a.Client == nil {
		a.Client = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := a.Client.Do(req)
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return out, fmt.Errorf("serveur: statut %d", resp.StatusCode)
	}
	return out, json.NewDecoder(resp.Body).Decode(&out)
}

func (a *Agent) apply(resp protocol.ReportResponse) {
	for _, c := range resp.Commands {
		log.Printf("commande reçue: %s", c.Type)
		switch c.Type {
		case protocol.CmdStopMiner:
			a.stopped = true
			a.stopMiner()
		case protocol.CmdRestartMiner:
			a.stopped = false
			a.stopMiner()
		case protocol.CmdReboot:
			a.stopMiner()
			if err := a.Reboot(); err != nil {
				log.Printf("redémarrage impossible: %v", err)
			}
		}
	}

	switch {
	case resp.FlightSheet == nil:
		if a.current != nil {
			log.Printf("plus de flight sheet: arrêt du mineur")
			a.stopMiner()
			a.current = nil
		}
		return
	case a.current == nil || *a.current != *resp.FlightSheet:
		log.Printf("nouvelle flight sheet: %s", resp.FlightSheet.Name)
		a.stopMiner()
		fs := *resp.FlightSheet
		a.current = &fs
	}

	m := a.miner()
	if m == nil {
		log.Printf("mineur %q non disponible sur ce rig", a.current.Miner)
		return
	}
	// Start (or restart after a crash) unless the user stopped mining.
	if !a.stopped && !m.Running() {
		if err := m.Start(*a.current); err != nil {
			log.Printf("démarrage du mineur impossible: %v", err)
		}
	}
}

// systemUptime reads /proc/uptime on Linux; elsewhere it returns 0.
func systemUptime() int64 {
	b, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	secs, _, _ := strings.Cut(string(b), ".")
	n, _ := strconv.ParseInt(strings.TrimSpace(secs), 10, 64)
	return n
}
