package tunnel

import (
	"encoding/json"
	"fmt"
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
	outbound["tag"] = "ai-vpn"
	cfg := map[string]any{
		"log": map[string]any{"disabled": true},
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
		return nil, fmt.Errorf("VPN config is not valid JSON: %w", err)
	}
	if typ, _ := root["type"].(string); strings.TrimSpace(typ) != "" {
		if !usableOutboundType(typ) {
			return nil, fmt.Errorf("VPN outbound type %q does not create a tunnel", typ)
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
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "", "direct", "block", "dns":
		return false
	default:
		return true
	}
}

func cloneObject(in map[string]any) map[string]any {
	b, _ := json.Marshal(in)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return out
}
