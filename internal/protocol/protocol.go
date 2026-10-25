// Package protocol defines the messages exchanged between the rig agent and the server.
package protocol

// GPUStat is one GPU's state as measured on the rig.
type GPUStat struct {
	Index       int     `json:"index"`
	Name        string  `json:"name"`
	Vendor      string  `json:"vendor"` // "nvidia" or "amd"
	TempC       float64 `json:"temp_c"`
	FanPct      float64 `json:"fan_pct"`
	PowerW      float64 `json:"power_w"`
	CoreMHz     int     `json:"core_mhz"`
	MemMHz      int     `json:"mem_mhz"`
	HashrateMHs float64 `json:"hashrate_mhs"`
}

// Report is sent by the agent every few seconds.
type Report struct {
	Hostname         string    `json:"hostname"`
	AgentVersion     string    `json:"agent_version"`
	UptimeS          int64     `json:"uptime_s"`
	MinerRunning     bool      `json:"miner_running"`
	Miner            string    `json:"miner"`
	TotalHashrateMHs float64   `json:"total_hashrate_mhs"`
	FlightSheetID    int64     `json:"flight_sheet_id"`
	GPUs             []GPUStat `json:"gpus"`
}

// FlightSheet says what a rig should mine, where and for whom.
type FlightSheet struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Miner     string `json:"miner"` // only "lolminer" in the MVP
	Algo      string `json:"algo"`
	Pool      string `json:"pool"`
	Wallet    string `json:"wallet"`
	Worker    string `json:"worker"`
	ExtraArgs string `json:"extra_args"`
}

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
