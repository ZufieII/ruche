//go:build !windows

package agent

import (
	"os/exec"
	"syscall"
)

// The miner gets its own process group so its children are stopped with it.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func terminate(cmd *exec.Cmd) { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM) }

func kill(cmd *exec.Cmd) { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }

func rebootCommand() *exec.Cmd { return exec.Command("systemctl", "reboot") }
