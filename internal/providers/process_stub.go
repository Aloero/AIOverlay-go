//go:build !windows

package providers

import "os/exec"

func configureHiddenChild(cmd *exec.Cmd) {}
