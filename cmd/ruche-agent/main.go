// Command ruche-agent runs on a mining rig and connects it to a ruche server.
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"

	"ruche/internal/agent"
	"ruche/internal/protocol"
)

// defaults returns where things live: system paths on Linux, next to the
// executable on Windows so the agent runs from a plain folder.
func defaults() (config, xmrig, lolminer, logs string) {
	if runtime.GOOS == "windows" {
		exe, _ := os.Executable()
		dir := filepath.Dir(exe)
		return filepath.Join(dir, "rig.conf"), filepath.Join(dir, "xmrig", "xmrig.exe"),
			filepath.Join(dir, "lolminer", "lolMiner.exe"), filepath.Join(dir, "logs")
	}
	return "/etc/ruche/rig.conf", "/opt/ruche/miners/xmrig/xmrig",
		"/opt/ruche/miners/lolminer/lolMiner", "/var/log/ruche"
}

func main() {
	defConfig, defXMRig, defLol, defLogs := defaults()
	configPath := flag.String("config", defConfig, "fichier de configuration du rig")
	xmrigBin := flag.String("xmrig", defXMRig, "chemin du binaire XMRig (minage CPU)")
	lolBin := flag.String("lolminer", defLol, "chemin du binaire lolMiner (minage GPU)")
	logDir := flag.String("log-dir", defLogs, "dossier des logs des mineurs")
	fakeCPU := flag.Bool("fake-cpu", false, "simule un processeur et XMRig (démo sans miner)")
	fakeGPUs := flag.Int("fake-gpus", 0, "simule N GPU et lolMiner (démo sans matériel)")
	flag.Parse()

	cfg, err := agent.LoadConfig(*configPath)
	if err != nil {
		log.Fatal(err)
	}

	a := &agent.Agent{Cfg: cfg, Miners: map[string]agent.Miner{}}
	if *fakeCPU || *fakeGPUs > 0 {
		a.CPU = agent.FakeCPU{}
		a.Miners[protocol.MinerXMRig] = &agent.FakeMiner{}
		if *fakeGPUs > 0 {
			a.GPUs = agent.FakeGPUs{Count: *fakeGPUs}
			a.Miners[protocol.MinerLolMiner] = &agent.FakeMiner{GPUs: *fakeGPUs}
		}
		a.Reboot = func() error { log.Print("(simulation) redémarrage"); return nil }
	} else {
		a.CPU = agent.SystemCPU{Root: "/"}
		a.GPUs = agent.SystemGPUs{SysfsRoot: "/sys"}
		_ = os.MkdirAll(*logDir, 0o755)
		a.Miners[protocol.MinerXMRig] = agent.NewXMRig(*xmrigBin, *logDir)
		a.Miners[protocol.MinerLolMiner] = agent.NewLolMiner(*lolBin, *logDir)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	log.Printf("ruche-agent %s connecté à %s", agent.Version, cfg.Server)
	a.Run(ctx)
}
