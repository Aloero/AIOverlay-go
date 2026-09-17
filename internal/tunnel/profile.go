package tunnel

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"
)

const EngineSingBox = "sing-box"

// BuildConfig accepts either one sing-box outbound object or a full sing-box
// config containing exactly one usable outbound. Inbounds, route rules, DNS,
// TUN and system-proxy settings from pasted configs are intentionally ignored:
// AIOverlay owns the local listener and only exposes an authenticated loopback
// proxy to its own provider clients.
func BuildConfig(raw string, port int, username, password string) ([]byte, error) {
	outbound, err := extractOutbound(raw)
	if err != nil {
		return nil, err
	}
	if port < 1 || port > 65535 || username == "" || password == "" {
		return nil, fmt.Errorf("invalid private proxy listener")
	}
	var original map[string]any
	_ = json.Unmarshal([]byte(raw), &original)
	dns, err := prepareDNS(original, outbound)
	if err != nil {
		return nil, err
	}
	outbound["tag"] = "ai-vpn"
	cfg := map[string]any{
		"log": map[string]any{"disabled": true},
		"dns": dns,
		"inbounds": []any{map[string]any{
			"type":             "mixed",
			"tag":              "ai-only-in",
			"listen":           "127.0.0.1",
			"listen_port":      port,
			"set_system_proxy": false,
			"users": []any{map[string]any{
				"username": username,
				"password": password,
			}},
		}},
		"outbounds": []any{outbound},
		"route": map[string]any{
			"final": "ai-vpn",
		},
	}
	return json.MarshalIndent(cfg, "", "  ")
}

func extractOutbound(raw string) (map[string]any, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("VPN config is empty")
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(raw), &root); err != nil {
		return nil, fmt.Errorf("VPN config is not valid JSON")
	}
	if typ, _ := root["type"].(string); strings.TrimSpace(typ) != "" {
		if !usableOutboundType(typ) {
			return nil, fmt.Errorf("VPN outbound type is unsupported or does not create a tunnel")
		}
		return cloneObject(root), nil
	}
	rawOut, ok := root["outbounds"].([]any)
	if !ok {
		return nil, fmt.Errorf("paste one sing-box outbound JSON object or a config with one outbound")
	}
	var candidates []map[string]any
	for _, item := range rawOut {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		typ, _ := obj["type"].(string)
		if usableOutboundType(typ) {
			candidates = append(candidates, cloneObject(obj))
		}
	}
	if len(candidates) != 1 {
		return nil, fmt.Errorf("VPN config must contain exactly one tunnel outbound; found %d", len(candidates))
	}
	return candidates[0], nil
}

func usableOutboundType(t string) bool {
	switch t {
	case "vless", "vmess", "trojan", "shadowsocks", "shadowtls", "hysteria", "hysteria2", "tuic", "ssh", "socks", "http":
		return true
	default:
		return false
	}
}

func cloneObject(in map[string]any) map[string]any {
	b, _ := json.Marshal(in)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return out
}

// Preserve DNS dependencies without importing inbounds, routes, TUN or services.
// Only the connection to the VPN endpoint is forced to IPv4. Target domain names
// stay inside the application proxy request and are resolved by the selected tunnel.
func prepareDNS(original, outbound map[string]any) (map[string]any, error) {
	if detour, ok := outbound["detour"].(string); ok && detour != "" {
		return nil, fmt.Errorf("VPN outbound detour requires another outbound; supply one standalone tunnel")
	}
	host, _ := outbound["server"].(string)
	if ip := net.ParseIP(host); ip != nil && ip.To4() == nil {
		return nil, fmt.Errorf("VPN endpoint must support IPv4; an IPv6 literal is not allowed")
	}
	if v, _ := outbound["inet6_bind_address"].(string); v != "" {
		return nil, fmt.Errorf("IPv6 bind is incompatible with the IPv4 tunnel policy")
	}
	// domain_strategy was removed by the pinned 1.14 engine. The explicit resolver
	// below carries the policy; the original portable profile is left unchanged.
	delete(outbound, "domain_strategy")
	dns := map[string]any{}
	if supplied, ok := original["dns"].(map[string]any); ok {
		dns = cloneObject(supplied)
	}
	servers, _ := dns["servers"].([]any)
	tags := map[string]bool{}
	for _, raw := range servers {
		server, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("invalid DNS server object")
		}
		tag, _ := server["tag"].(string)
		if tag != "" {
			if tags[tag] {
				return nil, fmt.Errorf("duplicate DNS server tag")
			}
			tags[tag] = true
		}
		if detour, _ := server["detour"].(string); detour != "" {
			return nil, fmt.Errorf("DNS bootstrap detour is not supported in a standalone tunnel; use a resolver reachable before tunnel startup")
		}
		endpoint, _ := server["server"].(string)
		if ip := net.ParseIP(endpoint); ip != nil && ip.To4() == nil {
			return nil, fmt.Errorf("DNS bootstrap must use an IPv4 endpoint")
		}
	}
	resolver := map[string]any{}
	switch v := outbound["domain_resolver"].(type) {
	case string:
		resolver["server"] = v
	case map[string]any:
		resolver = cloneObject(v)
	case nil:
	default:
		return nil, fmt.Errorf("invalid VPN domain resolver")
	}
	tag, _ := resolver["server"].(string)
	if tag == "" {
		tag = "ai-vpn-bootstrap"
		for tags[tag] {
			tag += "-local"
		}
		servers = append(servers, map[string]any{"type": "local", "tag": tag})
	} else if !tags[tag] {
		return nil, fmt.Errorf("VPN domain resolver refers to a missing DNS server")
	}
	resolver["server"] = tag
	resolver["strategy"] = "ipv4_only"
	outbound["domain_resolver"] = resolver
	dns["servers"] = servers
	// Bootstrap lookups themselves must not select IPv6 when resolving DNS hosts.
	dns["strategy"] = "ipv4_only"
	return dns, nil
}
