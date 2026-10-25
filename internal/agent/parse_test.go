package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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
	st := s.stats()
	if st.TotalHs != 121.4e6 || len(st.PerGPUHs) != 2 || st.PerGPUHs[1] != 60.2e6 {
		t.Fatalf("got %+v", st)
	}

	// Kaspa-style output in Gh/s.
	raw = `{"Algorithms":[{"Performance_Factor":1000000000,"Total_Performance":1.5,"Worker_Performance":[1.5]}]}`
	s = lolMinerSummary{}
	json.Unmarshal([]byte(raw), &s)
	if st := s.stats(); st.TotalHs != 1.5e9 {
		t.Fatalf("got %v", st.TotalHs)
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

func TestXMRigSummary(t *testing.T) {
	raw := `{"hashrate":{"total":[6123.4,6100.2,null],"threads":[[510.1,null,null],[509.9,null,null]]},"hugepages":[1168,1168]}`
	var s xmrigSummary
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatal(err)
	}
	st := s.stats()
	if st.TotalHs != 6123.4 || st.CPUThreads != 2 || st.HugePagesPct != 100 {
		t.Fatalf("got %+v", st)
	}

	// Just started: 10 s average still null, huge pages half allocated.
	raw = `{"hashrate":{"total":[null,null,null],"threads":[]},"hugepages":[1,2]}`
	s = xmrigSummary{}
	json.Unmarshal([]byte(raw), &s)
	if st := s.stats(); st.TotalHs != 0 || st.HugePagesPct != 50 {
		t.Fatalf("got %+v", st)
	}

	// Older XMRig sends a bool.
	s = xmrigSummary{}
	json.Unmarshal([]byte(`{"hashrate":{"total":[100]},"hugepages":true}`), &s)
	if st := s.stats(); st.HugePagesPct != 100 || st.TotalHs != 100 {
		t.Fatalf("got %+v", st)
	}
}

func TestXMRigArgs(t *testing.T) {
	fs := protocol.FlightSheet{Algo: "rx/0", Pool: "pool.supportxmr.com:3333", Wallet: "4abc", Worker: "pc"}
	got := strings.Join(xmrigArgs(fs), " ")
	want := "--url pool.supportxmr.com:3333 --user 4abc --pass pc --algo rx/0 --http-host 127.0.0.1 --http-port 4067 --no-color --donate-level 1 --rig-id pc"
	if got != want {
		t.Fatalf("got %q", got)
	}
}

func TestSystemCPU(t *testing.T) {
	root := t.TempDir()
	write := func(p, s string) {
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(s), 0o644)
	}
	write(filepath.Join(root, "proc/cpuinfo"), "processor\t: 0\nvendor_id\t: AuthenticAMD\nmodel name\t: AMD Ryzen 5 5600X 6-Core Processor\n")
	write(filepath.Join(root, "sys/class/hwmon/hwmon0/name"), "nvme\n")
	write(filepath.Join(root, "sys/class/hwmon/hwmon0/temp1_input"), "40000\n")
	write(filepath.Join(root, "sys/class/hwmon/hwmon1/name"), "k10temp\n")
	write(filepath.Join(root, "sys/class/hwmon/hwmon1/temp1_input"), "67250\n")

	c := SystemCPU{Root: root}.Read()
	if c.Model != "AMD Ryzen 5 5600X 6-Core Processor" || c.TempC != 67.25 || c.Threads == 0 {
		t.Fatalf("got %+v", c)
	}
}
