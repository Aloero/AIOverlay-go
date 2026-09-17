package engine

import "aioverlay/internal/domain"

type EffectiveLimits struct {
	ConnectMS       int `json:"connect_timeout_ms"`
	FirstTokenMS    int `json:"first_token_timeout_ms"`
	ResponseMS      int `json:"response_timeout_ms"`
	IdleMS          int `json:"stream_idle_timeout_ms"`
	MaxOutputTokens int `json:"max_output_tokens"`
}

func ResolveLimits(m domain.ModelSlot, b domain.ExecutionBudget, p domain.RequestDefaults) EffectiveLimits {
	return EffectiveLimits{firstPositive(b.ConnectTimeoutMS, p.ConnectTimeoutMS, 3000), firstPositive(m.FirstTokenTimeoutMS, b.FirstTokenTimeoutMS, p.FirstTokenTimeoutMS, 8000), firstPositive(m.TimeoutMS, b.ResponseTimeoutMS, p.ResponseTimeoutMS, 15000), firstPositive(b.StreamIdleTimeoutMS, p.StreamIdleTimeoutMS, 5000), firstPositive(p.MaxOutputTokens, 2048)}
}

// Kept beside ResolveLimits so source labels and actual values cannot diverge.
func LimitSources(m domain.ModelSlot, b domain.ExecutionBudget, p domain.RequestDefaults) map[string]string {
	source := func(slot, pipeline, provider int) string {
		if slot > 0 {
			return "model"
		}
		if pipeline > 0 {
			return "pipeline"
		}
		if provider > 0 {
			return "provider"
		}
		return "default"
	}
	return map[string]string{"connect_timeout_ms": source(0, b.ConnectTimeoutMS, p.ConnectTimeoutMS), "first_token_timeout_ms": source(m.FirstTokenTimeoutMS, b.FirstTokenTimeoutMS, p.FirstTokenTimeoutMS), "response_timeout_ms": source(m.TimeoutMS, b.ResponseTimeoutMS, p.ResponseTimeoutMS), "stream_idle_timeout_ms": source(0, b.StreamIdleTimeoutMS, p.StreamIdleTimeoutMS), "max_output_tokens": source(0, 0, p.MaxOutputTokens)}
}
