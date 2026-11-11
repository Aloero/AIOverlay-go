package app

import (
	"fmt"

	"aioverlay/internal/domain"
)

// recordAttemptEvidence is the single place a finished run changes what the application is willing to
// claim about models: the recent-usage history for the UI, and the catalog's observed outcomes, which
// gate routing. Both come from the same trace, and one call site means a future caller cannot refresh
// the history while silently skipping the evidence.
func (a *App) recordAttemptEvidence(t domain.ExecutionTrace) {
	a.recordRecentUsage(t)
	a.recordObservations(t)
}

// observedOutcomes are the ones worth remembering after a run ends. Whether each one *blocks* is
// decided by the catalog's classification, not here: a timeout is kept as history — "the last attempt
// timed out" is a fact a readiness report should show — but it says nothing about the model itself,
// so it never retires one.
var observedOutcomes = map[domain.Outcome]bool{
	domain.OutcomeSuccess:             true,
	domain.OutcomeModelUnavailable:    true,
	domain.OutcomeQuotaExceeded:       true,
	domain.OutcomeRateLimited:         true,
	domain.OutcomeProviderUnavailable: true,
	domain.OutcomeTimeout:             true,
}

// recordObservations feeds what an adapter actually experienced back into the catalog, so the next
// answer to "can I use this model?" is not based purely on files. This is the difference between the
// runtime refusing a retired model in a log nobody reads, and Studio marking that model unavailable
// with the refusal as the reason.
func (a *App) recordObservations(t domain.ExecutionTrace) {
	if a.catalog == nil {
		return
	}
	a.mu.Lock()
	providers := append([]domain.ProviderConfig(nil), a.cfg.Providers...)
	a.mu.Unlock()
	by := map[string]domain.ProviderConfig{}
	for _, p := range providers {
		by[p.ID] = p
	}
	for _, ev := range t.Events {
		if ev.Kind != "attempt_end" || ev.Attempt == nil || !observedOutcomes[ev.Outcome] {
			continue
		}
		p, ok := by[ev.Attempt.ProviderID]
		if !ok {
			continue
		}
		text := ev.Message
		if ev.Outcome == domain.OutcomeSuccess {
			text = fmt.Sprintf("answered in %d ms", ev.DurationMS)
		}
		a.catalog.Observe(p.ID, p.ObservedRef(ev.Attempt.ModelID), ev.Outcome, text)
	}
}
