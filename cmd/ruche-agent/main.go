// Command ruche-agent runs on a mining rig and connects it to a ruche server.
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"ruche/internal/agent"
)

func main() {
	configPath := flag.String("config", "/etc/ruche/rig.conf", "fichier de configuration du rig")
	minerBin := flag.String("lolminer", "/opt/ruche/miners/lolminer/lolMiner", "chemin du binaire lolMiner")
	logDir := flag.String("log-dir", "/var/log/ruche", "dossier des logs du mineur")
	fake := flag.Int("fake-gpus", 0, "simule N GPU et un mineur (démo sans matériel)")
	flag.Parse()

	cfg, err := agent.LoadConfig(*configPath)
	if err != nil {
		log.Fatal(err)
	}

	a := &agent.Agent{Cfg: cfg}
	if *fake > 0 {
		a.GPUs = agent.FakeGPUs{Count: *fake}
		a.Miner = &agent.FakeMiner{GPUs: *fake}
		a.Reboot = func() error { log.Print("(simulation) redémarrage"); return nil }
	} else {
		a.GPUs = agent.SystemGPUs{SysfsRoot: "/sys"}
		_ = os.MkdirAll(*logDir, 0o755)
		a.Miner = &agent.LolMiner{Binary: *minerBin, LogDir: *logDir}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	log.Printf("ruche-agent %s connecté à %s", agent.Version, cfg.Server)
	a.Run(ctx)
}
