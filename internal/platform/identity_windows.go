//go:build windows

package platform

import (
	"os"
	"strings"
	"syscall"
	"unicode"
	"unsafe"
)

var shell32Identity = syscall.NewLazyDLL("shell32.dll")
var setCurrentProcessExplicitAppUserModelID = shell32Identity.NewProc("SetCurrentProcessExplicitAppUserModelID")

// ConfigureProcessIdentity gives the host and WebView2 descendants one stable
// application identity. Task Manager can then present them as one expandable
// application group instead of unrelated windows/background helpers.
func ConfigureProcessIdentity(company, product string) string {
	clean := func(s string) string {
		var b strings.Builder
		for _, r := range strings.TrimSpace(s) {
			if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '-' || r == '_' {
				b.WriteRune(r)
			}
		}
		if b.Len() == 0 {
			return "AIOverlay"
		}
		return b.String()
	}
	id := clean(company) + "." + clean(product)
	if p, err := syscall.UTF16PtrFromString(id); err == nil {
		setCurrentProcessExplicitAppUserModelID.Call(uintptr(unsafe.Pointer(p)))
	}
	// Chromium/WebView2 understands this Windows switch; keep any caller args.
	args := strings.TrimSpace(os.Getenv("WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS"))
	if !strings.Contains(args, "--app-user-model-id=") {
		if args != "" {
			args += " "
		}
		_ = os.Setenv("WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS", args+"--app-user-model-id="+id)
	}
	return id
}
