//go:build !windows

package providers

import (
	"os/exec"
	"syscall"
)

// startGroup puts the CLI child in its own process group and makes cancellation signal the
// whole group: the harness spawns helper processes, and killing only the direct child would
// leave them holding the stdout pipe open past the cancel.
func startGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
}
