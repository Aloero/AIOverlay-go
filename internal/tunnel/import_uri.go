package tunnel

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// shareProfile owns only local parsing/build state, never a runtime process.
type shareProfile struct {
	url      *url.URL
	query    url.Values
	outbound map[string]any
	auth     string
	kind     string
}

func parseShareProfile(raw string) (*shareProfile, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.Opaque != "" || strings.ContainsAny(u.Hostname(), "\r\n \t") {
		return nil, fmt.Errorf("invalid VPN share link")
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return nil, fmt.Errorf("invalid share-link parameters")
	}
	for _, values := range q {
		if len(values) != 1 {
			return nil, fmt.Errorf("duplicate share-link parameters")
		}
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "vless" && scheme != "hysteria2" && scheme != "hy2" && scheme != "trojan" {
		return nil, fmt.Errorf("supported links: vless://, hysteria2://, hy2://, trojan://; other profiles require sing-box JSON")
	}
	if u.Path != "" && u.Path != "/" {
		return nil, fmt.Errorf("unexpected URI path; transport path belongs in the path parameter")
	}
	port := 443
	if u.Port() != "" {
		port, err = strconv.Atoi(u.Port())
		if err != nil || port < 1 || port > 65535 {
			return nil, fmt.Errorf("invalid server port; port hopping requires JSON")
		}
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil && ip.To4() == nil {
		return nil, fmt.Errorf("VPN endpoint must support IPv4")
	}
	auth := ""
	if u.User != nil {
		auth = u.User.Username()
		if pass, ok := u.User.Password(); ok {
			auth += ":" + pass
		}
	}
	if auth == "" {
		return nil, fmt.Errorf("share link has no authentication")
	}
	typ := scheme
	if scheme == "hy2" {
		typ = "hysteria2"
	}
	outbound := map[string]any{"type": typ, "server": u.Hostname(), "server_port": port}
	if strings.ContainsAny(auth, "\x00\r\n") {
		return nil, fmt.Errorf("invalid authentication text")
	}
	if typ == "trojan" && (q.Get("encryption") != "" || q.Get("flow") != "") {
		return nil, fmt.Errorf("VLESS-only parameters are not supported for Trojan")
	}
	if typ == "hysteria2" && q.Get("obfs-password") != "" && q.Get("obfs") == "" {
		return nil, fmt.Errorf("obfuscation type is required")
	}
	if q.Get("allowInsecure") != "" && q.Get("insecure") != "" && q.Get("allowInsecure") != q.Get("insecure") {
		return nil, fmt.Errorf("conflicting TLS verification flags")
	}
	return &shareProfile{url: u, query: q, outbound: outbound, auth: auth, kind: typ}, nil
}
