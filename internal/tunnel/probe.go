package tunnel

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

type ProbeResult struct {
	Target     string    `json:"target"`
	HTTPStatus int       `json:"http_status"`
	DurationMS int64     `json:"duration_ms"`
	CheckedAt  time.Time `json:"checked_at"`
}

// Probe is only called after an explicit user action. It makes one HTTPS HEAD
// request, sends no API key/prompt/image, does not follow redirects or fall back.
func (m *Manager) Probe(ctx context.Context, target string) (ProbeResult, error) {
	if m == nil || !m.Enabled() {
		return ProbeResult{}, fmt.Errorf("save and enable AI VPN before testing the connection")
	}
	u, err := url.Parse(target)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return ProbeResult{}, fmt.Errorf("use an HTTPS origin without credentials, path, query or fragment")
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	start := time.Now()
	proxy, err := m.ProxyURL(ctx)
	if err != nil {
		return ProbeResult{}, fmt.Errorf("AI VPN could not start; no direct request was sent")
	}
	if proxy == "" {
		return ProbeResult{}, fmt.Errorf("AI VPN is unavailable; no direct request was sent")
	}
	pu, err := url.Parse(proxy)
	if err != nil {
		return ProbeResult{}, fmt.Errorf("private proxy is unavailable")
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = http.ProxyURL(pu)
	tr.TLSHandshakeTimeout = 5 * time.Second
	defer tr.CloseIdleConnections()
	client := http.Client{Transport: tr, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, u.String(), nil)
	if err != nil {
		return ProbeResult{}, fmt.Errorf("invalid connection test URL")
	}
	resp, err := client.Do(req)
	if err != nil {
		return ProbeResult{}, fmt.Errorf("connection through AI VPN failed; no direct fallback was used")
	}
	defer resp.Body.Close()
	return ProbeResult{Target: u.Scheme + "://" + u.Host, HTTPStatus: resp.StatusCode, DurationMS: time.Since(start).Milliseconds(), CheckedAt: time.Now()}, nil
}
