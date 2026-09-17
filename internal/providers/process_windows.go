//go:build windows

package providers

import (
	"os/exec"
	"syscall"
)

const createNoWindow = 0x08000000

func configureHiddenChild(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}
