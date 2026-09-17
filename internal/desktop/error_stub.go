//go:build !windows

package desktop

import "log"

func ShowError(title, message string) { log.Printf("%s: %s", title, message) }
