//go:build !windows

package agent

func platformCPUModel() string { return "" }
