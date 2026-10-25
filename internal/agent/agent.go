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
	"os/exec"
	"strings"
	"time"

	"ruche/internal/protocol"
)

const Version = "0.1.0"

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
	GPUs   GPUReader
	Miner  Miner
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
		a.Reboot = func() error { return exec.Command("systemctl", "reboot").Run() }
	}
	t := time.NewTicker(a.Cfg.Interval)
	defer t.Stop()
	for {
		if err := a.Tick(ctx); err != nil {
			log.Printf("rapport échoué: %v", err)
		}
		select {
		case <-ctx.Done():
			a.Miner.Stop()
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

func (a *Agent) report(ctx context.Context) protocol.Report {
	host, _ := os.Hostname()
	gpus := a.GPUs.Read()
	total, per := a.Miner.Hashrates(ctx)
	for i := range gpus {
		if i < len(per) {
			gpus[i].HashrateMHs = per[i]
		}
	}
	rep := protocol.Report{
		Hostname:         host,
		AgentVersion:     Version,
		UptimeS:          int64(time.Since(a.started).Seconds()),
		MinerRunning:     a.Miner.Running(),
		TotalHashrateMHs: total,
		GPUs:             gpus,
	}
	if a.current != nil {
		rep.Miner = a.current.Miner
		rep.FlightSheetID = a.current.ID
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
			a.Miner.Stop()
		case protocol.CmdRestartMiner:
			a.stopped = false
			a.Miner.Stop()
		case protocol.CmdReboot:
			a.Miner.Stop()
			if err := a.Reboot(); err != nil {
				log.Printf("redémarrage impossible: %v", err)
			}
		}
	}

	switch {
	case resp.FlightSheet == nil:
		if a.current != nil {
			log.Printf("plus de flight sheet: arrêt du mineur")
			a.Miner.Stop()
			a.current = nil
		}
		return
	case a.current == nil || *a.current != *resp.FlightSheet:
		log.Printf("nouvelle flight sheet: %s", resp.FlightSheet.Name)
		fs := *resp.FlightSheet
		a.current = &fs
		a.Miner.Stop()
	}

	// Start (or restart after a crash) unless the user stopped mining.
	if !a.stopped && !a.Miner.Running() {
		if err := a.Miner.Start(*a.current); err != nil {
			log.Printf("démarrage du mineur impossible: %v", err)
		}
	}
}
