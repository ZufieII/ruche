package agent

import (
	"bytes"
	"encoding/csv"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"ruche/internal/protocol"
)

// GPUReader returns the current state of every GPU on the rig.
type GPUReader interface {
	Read() []protocol.GPUStat
}

// SystemGPUs reads NVIDIA cards through nvidia-smi and AMD cards through sysfs.
type SystemGPUs struct {
	SysfsRoot string // "/sys" outside of tests
}

func (s SystemGPUs) Read() []protocol.GPUStat {
	gpus := readNvidia()
	for _, g := range readAMD(s.SysfsRoot) {
		g.Index = len(gpus)
		gpus = append(gpus, g)
	}
	return gpus
}

func readNvidia() []protocol.GPUStat {
	if _, err := exec.LookPath("nvidia-smi"); err != nil {
		return nil
	}
	out, err := exec.Command("nvidia-smi",
		"--query-gpu=index,name,temperature.gpu,fan.speed,power.draw,clocks.sm,clocks.mem",
		"--format=csv,noheader,nounits").Output()
	if err != nil {
		return nil
	}
	return parseNvidiaCSV(out)
}

func parseNvidiaCSV(out []byte) []protocol.GPUStat {
	r := csv.NewReader(bytes.NewReader(out))
	r.TrimLeadingSpace = true
	records, err := r.ReadAll()
	if err != nil {
		return nil
	}
	var gpus []protocol.GPUStat
	for _, rec := range records {
		if len(rec) < 7 {
			continue
		}
		idx, _ := strconv.Atoi(rec[0])
		gpus = append(gpus, protocol.GPUStat{
			Index:   idx,
			Name:    rec[1],
			Vendor:  "nvidia",
			TempC:   num(rec[2]),
			FanPct:  num(rec[3]),
			PowerW:  num(rec[4]),
			CoreMHz: int(num(rec[5])),
			MemMHz:  int(num(rec[6])),
		})
	}
	return gpus
}

// num parses nvidia-smi values, which can be "[N/A]" on some cards.
func num(s string) float64 {
	f, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return f
}

func readAMD(sysfs string) []protocol.GPUStat {
	cards, _ := filepath.Glob(filepath.Join(sysfs, "class/drm/card[0-9]*/device/vendor"))
	sort.Strings(cards)
	var gpus []protocol.GPUStat
	for _, vendorFile := range cards {
		if readTrim(vendorFile) != "0x1002" {
			continue
		}
		dev := filepath.Dir(vendorFile)
		g := protocol.GPUStat{Vendor: "amd", Name: "AMD " + readTrim(filepath.Join(dev, "device"))}
		if hw, _ := filepath.Glob(filepath.Join(dev, "hwmon/hwmon*")); len(hw) > 0 {
			g.TempC = readNum(filepath.Join(hw[0], "temp1_input")) / 1000
			g.PowerW = readNum(filepath.Join(hw[0], "power1_average")) / 1e6
			if pwm := readNum(filepath.Join(hw[0], "pwm1")); pwm > 0 {
				g.FanPct = pwm * 100 / 255
			}
		}
		g.CoreMHz = activeClock(filepath.Join(dev, "pp_dpm_sclk"))
		g.MemMHz = activeClock(filepath.Join(dev, "pp_dpm_mclk"))
		gpus = append(gpus, g)
	}
	return gpus
}

// activeClock finds the line marked with '*' in a pp_dpm_* file, e.g. "1: 1340Mhz *".
func activeClock(path string) int {
	for _, line := range strings.Split(readTrim(path), "\n") {
		if !strings.HasSuffix(strings.TrimSpace(line), "*") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			mhz, _ := strconv.Atoi(strings.TrimSuffix(strings.ToLower(fields[1]), "mhz"))
			return mhz
		}
	}
	return 0
}

func readTrim(path string) string {
	b, _ := os.ReadFile(path)
	return strings.TrimSpace(string(b))
}

func readNum(path string) float64 {
	f, _ := strconv.ParseFloat(readTrim(path), 64)
	return f
}

// FakeGPUs simulates cards so the whole chain can be tried without mining hardware.
type FakeGPUs struct{ Count int }

func (f FakeGPUs) Read() []protocol.GPUStat {
	gpus := make([]protocol.GPUStat, f.Count)
	for i := range gpus {
		gpus[i] = protocol.GPUStat{
			Index: i, Name: "Simulated RTX 3070", Vendor: "nvidia",
			TempC: 55 + rand.Float64()*10, FanPct: 60 + rand.Float64()*10, PowerW: 120 + rand.Float64()*10,
			CoreMHz: 1100, MemMHz: 7000,
		}
	}
	return gpus
}
