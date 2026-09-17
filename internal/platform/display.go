package platform

import "strings"

func displayText(s string) string { return strings.ReplaceAll(s, "\x00", "\ufffd") }
