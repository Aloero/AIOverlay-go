package engine

import (
	"aioverlay/internal/domain"
	"aioverlay/internal/providers"
	"encoding/json"
	"sort"
	"strings"
)

func (e *Engine) secrets() []string {
	var out []string
	for _, p := range e.cfg.Providers {
		for _, k := range p.CredentialPool.Keys {
			if k.Secret != "" {
				out = append(out, k.Secret)
			}
		}
		for _, v := range p.Headers {
			if v != "" {
				out = append(out, v)
			}
		}
	}
	var value any
	_ = json.Unmarshal(e.cfg.VPN.Config, &value)
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, v := range x {
				switch strings.ToLower(k) {
				case "password", "uuid", "private_key", "secret", "token", "api_key":
					if s, ok := v.(string); ok && s != "" {
						out = append(out, s)
					}
				}
				walk(v)
			}
		case []any:
			for _, v := range x {
				walk(v)
			}
		}
	}
	walk(value)
	sort.Slice(out, func(i, j int) bool { return len(out[i]) > len(out[j]) })
	return out
}

type redactor struct{ values []string }

func (e *Engine) safeText(s string) string { return (redactor{e.secrets()}).text(s) }
func (r redactor) text(s string) string {
	for _, key := range r.values {
		s = strings.ReplaceAll(s, key, "[redacted]")
	}
	return s
}
func (e *Engine) safeError(err error) error {
	if err == nil {
		return nil
	}
	text := e.safeText(err.Error())
	if len(text) > 2048 {
		text = text[:2048] + "…"
	}
	return &providers.RequestError{Outcome: providers.OutcomeOf(err), Message: text}
}
func (r redactor) event(ev domain.TraceEvent) domain.TraceEvent {
	ev = domain.CloneTraceEvent(ev)
	ev.Message = r.text(ev.Message)
	ev.NodeID = r.text(ev.NodeID)
	ev.EdgeID = r.text(ev.EdgeID)
	ev.PlanID = r.text(ev.PlanID)
	if a := ev.Attempt; a != nil {
		a.ID = r.text(a.ID)
		a.SlotID = r.text(a.SlotID)
		a.ProviderID = r.text(a.ProviderID)
		a.ModelID = r.text(a.ModelID)
		a.CredentialID = r.text(a.CredentialID)
		a.CredentialAlias = r.text(a.CredentialAlias)
		a.Transport = r.text(a.Transport)
	}
	return ev
}

// redactChunk holds only a suffix that could become a secret across chunk boundaries.
func (r redactor) chunk(pending, delta string) (safe, tail string) {
	s := r.text(pending + delta)
	hold := 0
	for _, key := range r.values {
		for n := 1; n < len(key) && n <= len(s); n++ {
			if n > hold && strings.HasSuffix(s, key[:n]) {
				hold = n
			}
		}
	}
	return s[:len(s)-hold], s[len(s)-hold:]
}
