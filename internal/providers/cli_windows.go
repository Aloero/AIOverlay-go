//go:build windows

package providers

import "os/exec"

// startGroup keeps default cancellation on Windows for v1: the CLI shim chain there is shallow,
// and the Job Object pattern the OpenCode host uses belongs to long-lived servers, not one-shots.
func startGroup(cmd *exec.Cmd) {}
