package engine

import (
	"fmt"

	"aioverlay/internal/domain"
	"aioverlay/internal/providers"
)

func (s *simulator) admit(n domain.Node, c *simChain) error {
	for c.keyIndex < len(c.keys) && (c.repeats > 0 || c.keysTried < c.maxKeys) {
		k := c.keys[c.keyIndex]
		until := s.until[c.model.ProviderID+"/"+k.ID]
		if until < 0 || until > s.now {
			c.keyIndex++
			c.repeats = 0
			continue
		}
		if s.r.calls.Load() >= 256 {
			s.halted = true
			c.outcome = domain.OutcomeCancelled
			return fmt.Errorf("simulation safety limit reached (256 calls)")
		}
		if err := s.ctx.Err(); err != nil {
			s.halted = true
			c.outcome = domain.OutcomeCancelled
			return err
		}
		err := s.r.admit(n, *n.ModelPool, c.model, k)
		if err != nil {
			c.outcome = domain.OutcomeCancelled
			if !terminalPoolBudget(err) {
				c.keyIndex++
				c.repeats = 0
				continue
			}
			s.emit(domain.TraceEvent{Kind: "budget_exhausted", NodeID: n.ID, BudgetScope: budgetScope(err), Message: err.Error()})
			return err
		}
		if c.repeats == 0 {
			c.keysTried++
		}
		c.repeats++
		c.ref = &domain.AttemptRef{ID: fmt.Sprintf("sim-%d", s.r.calls.Load()), SlotID: c.model.ID, ProviderID: c.model.ProviderID, ModelID: c.model.ModelID, CredentialID: k.ID, CredentialAlias: k.Label, Transport: "Direct"}
		if c.ref.CredentialAlias == "" {
			c.ref.CredentialAlias = k.ID
		}
		if label := domain.Policy(c.provider.Kind).TransportLabel; label != "" {
			c.ref.Transport = label
		}
		s.emit(domain.TraceEvent{Kind: "attempt_start", NodeID: n.ID, Attempt: c.ref})
		key := n.ID + "/" + c.model.ProviderID + "/" + c.model.ModelID
		s.modelAttempts[key]++
		script := SimulationOutcome{Outcome: domain.OutcomeSuccess, DurationMS: 1000, FirstTokenMS: 200, Text: "42"}
		for _, o := range s.req.Outcomes {
			if (o.NodeID == "" || o.NodeID == n.ID) && (o.ProviderID == "" || o.ProviderID == c.model.ProviderID) && (o.ModelID == "" || o.ModelID == c.model.ModelID) && (o.Attempt == 0 || o.Attempt == s.modelAttempts[key]) {
				script = o
				break
			}
		}
		lim := ResolveLimits(c.model, s.r.pipeline.Budget, c.provider.RequestDefaults)
		c.start = s.now
		c.finish = s.now + int64(script.DurationMS)
		c.first = 0
		c.emittedFirst = false
		c.outcome = script.Outcome
		c.text = script.Text
		if c.text == "" {
			c.text = "42"
		}
		// A simulated run must model the same delivery shape the real transport has, or the
		// first-token outcome it predicts would not be the one the user gets. The adapter is the
		// only authority on that, and answering costs no request.
		adapter, _ := s.r.engine.registry.Get(c.provider.ID)
		buffered := !providers.StreamsTokens(c.provider, adapter)
		if c.outcome == domain.OutcomeTimeout || script.DurationMS > lim.ResponseMS {
			c.finish = s.now + int64(lim.ResponseMS)
			c.outcome = domain.OutcomeTimeout
		}
		if c.outcome == domain.OutcomeFirstTokenTimeout {
			c.finish = s.now + int64(min(lim.FirstTokenMS, lim.ResponseMS))
		}
		if !buffered && script.FirstTokenMS > 0 && c.outcome != domain.OutcomeFirstTokenTimeout {
			if script.FirstTokenMS > lim.FirstTokenMS && s.now+int64(lim.FirstTokenMS) < c.finish {
				c.finish = s.now + int64(lim.FirstTokenMS)
				c.outcome = domain.OutcomeFirstTokenTimeout
			} else if s.now+int64(script.FirstTokenMS) <= c.finish {
				c.first = s.now + int64(script.FirstTokenMS)
			}
		}
		if c.outcome == domain.OutcomeStreamIdleTimeout {
			c.first = s.now + int64(max(1, script.FirstTokenMS))
			c.finish = min(s.now+int64(lim.ResponseMS), c.first+int64(lim.IdleMS))
		}
		return nil
	}
	c.outcome = domain.OutcomeAuthFailed
	return fmt.Errorf("no allowed healthy credentials in simulation")
}
func (s *simulator) advanceKey(c *simChain) bool {
	retry := c.outcome == domain.OutcomeTimeout || c.outcome == domain.OutcomeNetworkError || c.outcome == domain.OutcomeProviderUnavailable
	if retry && c.repeats < max(1, c.model.MaxAttempts) {
		return true
	}
	c.keyIndex++
	c.repeats = 0
	return c.keyIndex < len(c.keys)
}
func (s *simulator) failedKey(c *simChain) {
	id := c.model.ProviderID + "/" + c.ref.CredentialID
	switch c.outcome {
	case domain.OutcomeAuthFailed:
		s.until[id] = -1
	case domain.OutcomeRateLimited, domain.OutcomeQuotaExceeded:
		s.until[id] = s.now + int64(max(1, c.provider.CredentialPool.RateLimitCooldownS))*1000
	}
}
func (s *simulator) end(n domain.Node, c *simChain, outcome domain.Outcome, reason string) {
	ttft := int64(0)
	if c.emittedFirst {
		ttft = c.first - c.start
	}
	s.emit(domain.TraceEvent{Kind: "attempt_end", NodeID: n.ID, Attempt: c.ref, Outcome: outcome, Message: reason, TTFTMS: ttft, DurationMS: s.now - c.start})
}
