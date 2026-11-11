package app

import (
	"strings"
	"testing"

	"aioverlay/internal/domain"
)

// The report is the one place the user asks "why is it not ready" - so every part of the answer must
// come from a layer that actually knows it: the snapshot version, the connection verdicts, the pool's
// own model refs, and the watched outcomes.
func TestReadinessReportAggregatesEveryLayer(t *testing.T) {
	a := studioTestApp(t)
	service := evidenceCatalog(t)
	a.catalog = service

	report := a.Readiness()
	if report.Catalog.OfferedSnapshot != "9.9.9" {
		t.Fatalf("the report must name the published-list snapshot it consulted: %q", report.Catalog.OfferedSnapshot)
	}
	if report.Catalog.Models == 0 {
		t.Fatal("catalog model count missing")
	}

	byID := map[string]int{}
	for i, c := range report.Connections {
		byID[c.Adapter.ProviderID] = i
	}
	gateway, ok := byID["opencode"]
	if !ok || !report.Connections[gateway].Verdict.Ready {
		t.Fatalf("the keyless gateway with a runtime present must read usable: %+v", report.Connections)
	}
	relay, ok := byID["openrouter"]
	if !ok || report.Connections[relay].Verdict.Ready {
		t.Fatal("a relay connection with no configured key must not read ready")
	}
	codes := map[string]bool{}
	for _, blocker := range report.Connections[relay].Verdict.Blockers {
		codes[blocker.Code] = true
	}
	if !codes[domain.BlockNoKey] {
		t.Fatalf("the missing key must be the stated reason: %+v", report.Connections[relay].Verdict.Blockers)
	}

	// The default pool names four OpenCode refs; one is published (ready), the rest no connection
	// can route - the report must show both, with a reason on every blocked one.
	models := map[string]int{}
	for i, m := range report.Models {
		models[m.Ref] = i
	}
	if len(models) != 4 {
		t.Fatalf("every enabled pool slot must be reported: %+v", report.Models)
	}
	minimax := report.Models[models["opencode/minimax-m3.1-flash-free"]]
	if !minimax.Verdict.Ready {
		t.Fatalf("the published pool model must be ready: %+v", minimax.Verdict.Blockers)
	}
	if len(minimax.UsedBy) == 0 || !strings.Contains(minimax.UsedBy[0], "/") {
		t.Fatalf("the report must say which pool asked for the model: %+v", minimax.UsedBy)
	}
	ghost := report.Models[models["opencode/longcat-2.5-free"]]
	if ghost.Verdict.Ready || !strings.Contains(ghost.Reason, "no enabled connection") {
		t.Fatalf("a retired pool ref needs a stated reason: %+v", ghost)
	}

	// A watched refusal rewrites the answer for the very model the pool uses - this is the §6.3
	// quota incident as a unit: the user would have seen "timeout" with no author anywhere.
	a.recordAttemptEvidence(domain.ExecutionTrace{Events: []domain.TraceEvent{
		attemptEvent("opencode", "opencode/minimax-m3.1-flash-free", domain.OutcomeQuotaExceeded, "AI_APICallError: Free usage limit reached"),
	}})
	after := a.Readiness()
	seen := false
	for _, m := range after.Models {
		if m.Ref == "opencode/minimax-m3.1-flash-free" {
			seen = true
			if m.Verdict.Ready || !strings.Contains(m.Reason, "Free usage limit reached") {
				t.Fatalf("the upstream's own refusal must be the reported reason: %+v", m)
			}
			if m.Verdict.Evidence != domain.ProvenanceObserved {
				t.Fatalf("evidence=%q, want observed", m.Verdict.Evidence)
			}
		}
	}
	if !seen {
		t.Fatal("pool model disappeared from the report")
	}
	if len(after.Observations) == 0 || after.Observations[0].Outcome != domain.OutcomeQuotaExceeded {
		t.Fatalf("the history section must show the watched outcome: %+v", after.Observations)
	}
}
