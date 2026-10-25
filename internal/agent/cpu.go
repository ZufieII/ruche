package agent

import (
	"bufio"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"ruche/internal/protocol"
)

// CPUReader returns what the OS knows about the processor.
type CPUReader interface {
	Read() protocol.CPUStat
}

// SystemCPU reads /proc and hwmon on Linux. Elsewhere it falls back to the
// platform's model name and reports no temperature.
type SystemCPU struct {
	Root string // "/" outside of tests
}

func (s SystemCPU) Read() protocol.CPUStat {
	model := linuxCPUModel(filepath.Join(s.Root, "proc/cpuinfo"))
	if model == "" {
		model = platformCPUModel()
	}
	return protocol.CPUStat{
		Model:   model,
		Threads: runtime.NumCPU(),
		TempC:   linuxCPUTemp(filepath.Join(s.Root, "sys/class/hwmon")),
	}
}

func linuxCPUModel(cpuinfo string) string {
	f, err := os.Open(cpuinfo)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if key, val, ok := strings.Cut(sc.Text(), ":"); ok && strings.TrimSpace(key) == "model name" {
			return strings.TrimSpace(val)
		}
	}
	return ""
}

// linuxCPUTemp reads the package temperature from the AMD (k10temp, zenpower)
// or Intel (coretemp) sensor drivers. Their first input is the package sensor.
func linuxCPUTemp(hwmon string) float64 {
	dirs, _ := filepath.Glob(filepath.Join(hwmon, "hwmon*"))
	sort.Strings(dirs)
	for _, d := range dirs {
		switch readTrim(filepath.Join(d, "name")) {
		case "k10temp", "zenpower", "coretemp":
			return readNum(filepath.Join(d, "temp1_input")) / 1000
		}
	}
	return 0
}

// FakeCPU simulates a processor for demos.
type FakeCPU struct{}

func (FakeCPU) Read() protocol.CPUStat {
	return protocol.CPUStat{Model: "Simulated Ryzen 5 5600X", Threads: 12, TempC: 62 + rand.Float64()*6}
}
