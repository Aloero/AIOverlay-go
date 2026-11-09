package providers

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ProxyResolver is implemented by the application-scoped AI tunnel. An empty
// URL means normal networking. A resolver error is fail-closed and must never
// fall back to a direct request.
type ProxyResolver interface {
	ProxyURL(context.Context) (string, error)
}

func requestTransport(ctx context.Context, resolver ProxyResolver, connectTimeout time.Duration) (*http.Transport, error) {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if connectTimeout > 0 {
		tr.DialContext = (&net.Dialer{Timeout: connectTimeout}).DialContext
		tr.TLSHandshakeTimeout = connectTimeout
	}
	if resolver != nil {
		raw, err := resolver.ProxyURL(ctx)
		if err != nil {
			return nil, fmt.Errorf("AI VPN unavailable")
		}
		if raw != "" {
			u, err := url.Parse(raw)
			if err != nil {
				return nil, fmt.Errorf("AI VPN proxy URL is invalid")
			}
			tr.Proxy = http.ProxyURL(u)
		}
	}
	return tr, nil
}

// Provider redirects may keep their path-level routing, but must not forward
// credentials or request media to a different scheme, host, or effective port.
// Go's default policy does not protect custom API-key headers or POST bodies.
func checkProviderRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return fmt.Errorf("stopped after 10 redirects")
	}
	if len(via) == 0 {
		return nil
	}
	origin := via[0].URL
	if !strings.EqualFold(req.URL.Scheme, origin.Scheme) ||
		!strings.EqualFold(req.URL.Hostname(), origin.Hostname()) ||
		effectiveProviderPort(req.URL) != effectiveProviderPort(origin) {
		return http.ErrUseLastResponse
	}
	return nil
}

func effectiveProviderPort(u *url.URL) string {
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
