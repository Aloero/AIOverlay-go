//go:build !windows && !linux

package childprocess

import "os/exec"

func configure(cmd *exec.Cmd)               {}
func contain(cmd *exec.Cmd) (func(), error) { return func() {}, nil }
