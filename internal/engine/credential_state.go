package engine

import (
	"aioverlay/internal/domain"
	"fmt"
	"time"
)

// No secrets or request data leave the engine through this snapshot.
type CredentialState struct {
	ProviderID    string         `json:"provider_id"`
	CredentialID  string         `json:"credential_id"`
	Outcome       domain.Outcome `json:"outcome,omitempty"`
	CheckedAt     time.Time      `json:"checked_at,omitempty"`
	CooldownUntil time.Time      `json:"cooldown_until,omitempty"`
	Enabled       bool           `json:"enabled"`
}

func (e *Engine) CredentialStates() []CredentialState {
	e.configMu.RLock()
	defer e.configMu.RUnlock()
	e.mu.Lock()
	defer e.mu.Unlock()
	out := []CredentialState{}
	for _, p := range e.cfg.Providers {
		for _, k := range p.CredentialPool.Keys {
			s := e.credentialResults[p.ID+"/"+k.ID]
			s.ProviderID = p.ID
			s.CredentialID = k.ID
			s.Enabled = p.Enabled && k.Enabled
			s.CooldownUntil = e.cooldown[p.ID+"/"+k.ID]
			out = append(out, s)
		}
	}
	return out
}
func (e *Engine) ConfigRevision() uint64 {
	e.configMu.RLock()
	defer e.configMu.RUnlock()
	return e.revision
}

// SelectSessionCredential applies the same provider credential policy used by
// short model attempts, but returns one key for the lifetime of a persistent
// session. The caller must keep that key pinned until the session is replaced.
func (e *Engine) SelectSessionCredential(providerID string) (domain.Credential, error) {
	e.configMu.RLock()
	var pool domain.CredentialPool
	found := false
	for _, provider := range e.cfg.Providers {
		if provider.ID == providerID && provider.Enabled {
			pool = provider.CredentialPool
			found = true
			break
		}
	}
	e.configMu.RUnlock()
	if !found {
		return domain.Credential{}, fmt.Errorf("provider %s is missing or disabled", providerID)
	}
	keys := e.orderCredentials(providerID, pool)
	if len(keys) == 0 {
		return domain.Credential{}, fmt.Errorf("provider %s has no available credential", providerID)
	}
	return keys[0], nil
}

func (e *Engine) RecordSessionCredential(providerID string, key domain.Credential, outcome domain.Outcome) {
	e.configMu.RLock()
	cooldown := 0
	for _, provider := range e.cfg.Providers {
		if provider.ID == providerID {
			cooldown = provider.CredentialPool.RateLimitCooldownS
			break
		}
	}
	e.configMu.RUnlock()
	e.markCredential(providerID, key, outcome, cooldown)
}
