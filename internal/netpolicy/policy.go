// Package netpolicy holds the outbound network rules shared by every model transport, so
// "may this redirect carry our credentials" is not decided twice by two adapters.
package netpolicy

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// SameOriginRedirect allows path-level routing inside one origin but refuses to forward
// credentials or a request body to a different scheme, host or effective port. Go's default
// policy drops Authorization on cross-origin hops but keeps custom API-key headers, which is
// exactly the leak an adapter must not risk. Usable directly as http.Client.CheckRedirect.
func SameOriginRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return fmt.Errorf("stopped after 10 redirects")
	}
	if len(via) == 0 {
		return nil
	}
	origin := via[0].URL
	if !strings.EqualFold(req.URL.Scheme, origin.Scheme) ||
		!strings.EqualFold(req.URL.Hostname(), origin.Hostname()) ||
		EffectivePort(req.URL) != EffectivePort(origin) {
		return http.ErrUseLastResponse
	}
	return nil
}

// EffectivePort resolves the default port of a scheme so "https://x" and "https://x:443" compare equal.
func EffectivePort(u *url.URL) string {
	if port := u.Port(); port != "" {
		return port
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		return "443"
	case "http":
		return "80"
	default:
		return ""
	}
}
