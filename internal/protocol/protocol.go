// Package protocol defines the messages exchanged between the rig agent and the server.
package protocol

// GPUStat is one GPU's state as measured on the rig.
type GPUStat struct {
	Index      int     `json:"index"`
	Name       string  `json:"name"`
	Vendor     string  `json:"vendor"` // "nvidia" or "amd"
	TempC      float64 `json:"temp_c"`
	FanPct     float64 `json:"fan_pct"`
	PowerW     float64 `json:"power_w"`
	CoreMHz    int     `json:"core_mhz"`
	MemMHz     int     `json:"mem_mhz"`
	HashrateHs float64 `json:"hashrate_hs"`
}

// CPUStat is the processor's state. Mining fields are filled only while XMRig runs.
type CPUStat struct {
	Model         string  `json:"model"`
	Threads       int     `json:"threads"`
	TempC         float64 `json:"temp_c"` // 0 when the OS does not expose it
	HashrateHs    float64 `json:"hashrate_hs"`
	MiningThreads int     `json:"mining_threads"`
	HugePagesPct  float64 `json:"huge_pages_pct"`
}

// Report is sent by the agent every few seconds.
type Report struct {
	Hostname        string    `json:"hostname"`
	AgentVersion    string    `json:"agent_version"`
	UptimeS         int64     `json:"uptime_s"`        // agent uptime
	SystemUptimeS   int64     `json:"system_uptime_s"` // 0 when unknown
	IP              string    `json:"ip"`              // filled in by the server
	MinerRunning    bool      `json:"miner_running"`
	Miner           string    `json:"miner"`
	TotalHashrateHs float64   `json:"total_hashrate_hs"` // hashes per second
	Algo            string    `json:"algo"`
	FlightSheetID   int64     `json:"flight_sheet_id"`
	CPU             *CPUStat  `json:"cpu,omitempty"`
	GPUs            []GPUStat `json:"gpus"`
	MinerLog        []string  `json:"miner_log,omitempty"` // last lines of the miner's output
	// External is true when the agent only watches a miner it did not start
	// (for example one run by Hive OS). Pool is then what that miner reports.
	External bool   `json:"external,omitempty"`
	Pool     string `json:"pool,omitempty"`
}

// FlightSheet says what a rig should mine, where and for whom.
type FlightSheet struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Miner     string `json:"miner"` // MinerXMRig or MinerLolMiner
	Algo      string `json:"algo"`
	Pool      string `json:"pool"`
	Wallet    string `json:"wallet"`
	Worker    string `json:"worker"`
	ExtraArgs string `json:"extra_args"`
}

const (
	MinerXMRig    = "xmrig"    // CPU
	MinerLolMiner = "lolminer" // NVIDIA and AMD GPUs
)

// Command types the agent understands.
const (
	CmdRestartMiner = "restart_miner"
	CmdStopMiner    = "stop_miner"
	CmdReboot       = "reboot"
)

type Command struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

// ReportResponse carries the desired state back to the agent, so the agent
// only ever opens outbound connections.
type ReportResponse struct {
	FlightSheet *FlightSheet `json:"flight_sheet"`
	Commands    []Command    `json:"commands"`
}
