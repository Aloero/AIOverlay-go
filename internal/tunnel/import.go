package tunnel

// Share-link conversion rejects unsupported security and transport parameters.
import (
	"encoding/json"
	"fmt"
	"strings"
)

type ImportedProfile struct {
	Name     string          `json:"name"`
	Config   json.RawMessage `json:"config"`
	Protocol string          `json:"protocol"`
	Server   string          `json:"server"`
	Warnings []string        `json:"warnings"`
}

func ImportProfile(raw string) (ImportedProfile, error) {
	raw = strings.TrimSpace(raw)
	if len(raw) == 0 || len(raw) > 256<<10 {
		return ImportedProfile{}, fmt.Errorf("VPN profile is empty or exceeds 256 KiB")
	}
	result := ImportedProfile{Warnings: []string{}}
	var outbound map[string]any
	if strings.HasPrefix(raw, "{") {
		var err error
		outbound, err = extractOutbound(raw)
		if err != nil {
			return result, err
		}
		if _, err = BuildConfig(raw, 23456, "check", "check"); err != nil {
			return result, err
		}
		result.Config = json.RawMessage(raw)
		result.Name = "Основной"
	} else {
		var err error
		outbound, err = importShareProfile(raw, &result)
		if err != nil {
			return result, err
		}
	}
	result.Protocol, _ = outbound["type"].(string)
	result.Server, _ = outbound["server"].(string)
	return result, nil
}

func importShareProfile(raw string, result *ImportedProfile) (map[string]any, error) {
	link, err := parseShareProfile(raw)
	if err != nil {
		return nil, err
	}
	if err := link.configureProtocol(result); err != nil {
		return nil, err
	}
	u, outbound := link.url, link.outbound
	b, err := json.MarshalIndent(outbound, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("cannot encode VPN profile")
	}
	result.Config = b
	if _, err = BuildConfig(string(b), 23456, "check", "check"); err != nil {
		return nil, err
	}
	result.Name = u.Fragment
	if result.Name == "" {
		result.Name = "Основной"
	}
	if len(result.Name) > 120 || strings.ContainsRune(result.Name, 0) {
		return nil, fmt.Errorf("VPN profile name is too long or invalid")
	}
	return outbound, nil
}
