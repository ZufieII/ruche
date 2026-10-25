//go:build windows

package agent

import (
	"os/exec"
	"strings"
)

// platformCPUModel reads the name Windows shows in Task Manager.
func platformCPUModel() string {
	out, err := exec.Command("reg", "query", `HKLM\HARDWARE\DESCRIPTION\System\CentralProcessor\0`, "/v", "ProcessorNameString").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if _, val, ok := strings.Cut(line, "REG_SZ"); ok {
			return strings.TrimSpace(val)
		}
	}
	return ""
}
