//go:build windows

package agent

import "os/exec"

func setProcessGroup(*exec.Cmd) {}

// Windows has no SIGTERM for console programs we don't own: stop the miner directly.
func terminate(cmd *exec.Cmd) { _ = cmd.Process.Kill() }

func kill(cmd *exec.Cmd) { _ = cmd.Process.Kill() }

func rebootCommand() *exec.Cmd { return exec.Command("shutdown", "/r", "/t", "0") }
