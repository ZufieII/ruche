package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"ruche/internal/protocol"
)

func TestParseNvidiaCSV(t *testing.T) {
	out := []byte("0, NVIDIA GeForce RTX 3070, 61, 70, 118.52, 1110, 7000\n1, NVIDIA GeForce RTX 3060, 58, [N/A], 101.10, 1200, 7500\n")
	gpus := parseNvidiaCSV(out)
	if len(gpus) != 2 {
		t.Fatalf("got %d gpus", len(gpus))
	}
	if gpus[0].Name != "NVIDIA GeForce RTX 3070" || gpus[0].TempC != 61 || gpus[0].PowerW != 118.52 || gpus[0].MemMHz != 7000 {
		t.Fatalf("bad parse: %+v", gpus[0])
	}
	if gpus[1].FanPct != 0 {
		t.Fatalf("N/A fan should be 0, got %v", gpus[1].FanPct)
	}
}

func TestReadAMD(t *testing.T) {
	root := t.TempDir()
	dev := filepath.Join(root, "class/drm/card0/device")
	hw := filepath.Join(dev, "hwmon/hwmon3")
	os.MkdirAll(hw, 0o755)
	write := func(p, s string) { os.WriteFile(p, []byte(s), 0o644) }
	write(filepath.Join(dev, "vendor"), "0x1002\n")
	write(filepath.Join(dev, "device"), "0x73bf\n")
	write(filepath.Join(dev, "pp_dpm_sclk"), "0: 500Mhz\n1: 1340Mhz *\n2: 2100Mhz\n")
	write(filepath.Join(dev, "pp_dpm_mclk"), "0: 96Mhz\n3: 1000Mhz *\n")
	write(filepath.Join(hw, "temp1_input"), "64000\n")
	write(filepath.Join(hw, "power1_average"), "135000000\n")
	write(filepath.Join(hw, "pwm1"), "153\n")

	gpus := readAMD(root)
	if len(gpus) != 1 {
		t.Fatalf("got %d gpus", len(gpus))
	}
	g := gpus[0]
	if g.TempC != 64 || g.PowerW != 135 || g.CoreMHz != 1340 || g.MemMHz != 1000 || g.FanPct != 60 {
		t.Fatalf("bad read: %+v", g)
	}
}

func TestLolMinerSummary(t *testing.T) {
	raw := `{"Algorithms":[{"Algorithm":"Etchash","Performance_Unit":"Mh/s","Performance_Factor":1000000,
		"Total_Performance":121.4,"Worker_Performance":[61.2,60.2]}]}`
	var s lolMinerSummary
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatal(err)
	}
	total, per := s.hashrates()
	if total != 121.4 || len(per) != 2 || per[1] != 60.2 {
		t.Fatalf("got %v %v", total, per)
	}

	// Kaspa-style output in Gh/s becomes MH/s.
	raw = `{"Algorithms":[{"Performance_Factor":1000000000,"Total_Performance":1.5,"Worker_Performance":[1.5]}]}`
	s = lolMinerSummary{}
	json.Unmarshal([]byte(raw), &s)
	if total, _ := s.hashrates(); total != 1500 {
		t.Fatalf("got %v", total)
	}
}

func TestLolMinerArgs(t *testing.T) {
	args := lolMinerArgs(protocolFS())
	want := "--algo ETCHASH --pool etc.2miners.com:1010 --user 0xabc.rig1 --apiport 4068 --tstop 85"
	got := ""
	for i, a := range args {
		if i > 0 {
			got += " "
		}
		got += a
	}
	if got != want {
		t.Fatalf("got %q", got)
	}
}

func protocolFS() protocol.FlightSheet {
	return protocol.FlightSheet{Algo: "ETCHASH", Pool: "etc.2miners.com:1010", Wallet: "0xabc", Worker: "rig1", ExtraArgs: "--tstop 85"}
}
