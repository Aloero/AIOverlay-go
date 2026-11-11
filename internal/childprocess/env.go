package childprocess

import (
	"os"
	"strings"
)

// Keep only ordinary process-launch and TLS environment. In particular do not
// inherit OpenCode configs, credential files, provider secrets or proxy bypasses.
func SanitizedEnv() []string {
	allowed := map[string]bool{"PATH": true, "PATHEXT": true, "SYSTEMROOT": true, "WINDIR": true, "COMSPEC": true, "SYSTEMDRIVE": true, "TEMP": true, "TMP": true, "LANG": true, "LC_ALL": true, "TZ": true, "SSL_CERT_FILE": true, "SSL_CERT_DIR": true}
	out := []string{}
	for _, item := range os.Environ() {
		name, _, _ := strings.Cut(item, "=")
		if allowed[strings.ToUpper(name)] {
			out = append(out, item)
		}
	}
	return out
}
