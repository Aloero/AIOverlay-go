package session

import (
	"crypto/sha256"
	"encoding/json"

	"aioverlay/internal/domain"
)

type realtimeFingerprintInput struct {
	Provider *domain.ProviderConfig `json:"provider,omitempty"`
	VPN      domain.VPNConfig       `json:"vpn"`
}

func realtimeSourceFingerprint(cfg domain.Config, source domain.EventSource) [32]byte {
	if source.Realtime == nil || source.Realtime.ProviderID == "" {
		return [32]byte{}
	}
	var provider *domain.ProviderConfig
	for index := range cfg.Providers {
		if cfg.Providers[index].ID == source.Realtime.ProviderID {
			copy := cfg.Providers[index]
			provider = &copy
			break
		}
	}
	raw, err := json.Marshal(realtimeFingerprintInput{Provider: provider, VPN: cfg.VPN})
	if err != nil {
		return sha256.Sum256([]byte("invalid-realtime-route"))
	}
	return sha256.Sum256(raw)
}

func sourceRevoked(runtime *sourceRuntime) bool {
	if runtime == nil {
		return false
	}
	runtime.mu.RLock()
	defer runtime.mu.RUnlock()
	return runtime.revoked
}

func revokeSource(runtime *sourceRuntime) {
	if runtime == nil {
		return
	}
	runtime.mu.Lock()
	alreadyRevoked := runtime.revoked
	runtime.revoked = true
	runtime.mu.Unlock()
	if !alreadyRevoked && runtime.cancel != nil {
		runtime.cancel()
	}
}
