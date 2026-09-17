package engine

import (
	"aioverlay/internal/domain"
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
