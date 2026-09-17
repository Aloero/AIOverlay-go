//go:build !windows

package tunnel

import "os/exec"

func configureHiddenChild(cmd *exec.Cmd) {}
