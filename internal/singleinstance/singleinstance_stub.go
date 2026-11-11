//go:build !windows

package singleinstance

// Ensure is a no-op outside Windows. The mutex guards the shipped desktop
// scenario; Linux development and headless test hosts may intentionally run
// several copies against separate configurations.
func Ensure() bool { return true }
