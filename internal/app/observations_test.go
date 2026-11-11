package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"aioverlay/internal/catalog"
	"aioverlay/internal/domain"
)

// evidenceCatalog builds the smallest catalog that can express "the host publishes this free model",
// so the assertions below are about the wiring between a finished attempt and what the app is willing
// to claim - not about which models happen to be free upstream today.
func evidenceCatalog(t *testing.T) *catalog.Service {
	t.Helper()
	dir := t.TempDir()
	// One ref matches the shipped default pool, so the readiness report has a real "model this app
	// actually wants" to judge; the other pool refs stay absent and must surface as no_route.
	catalogJSON := `{"opencode":{"name":"OpenCode Zen","models":{"free-vision":{"name":"Free Vision",` +
		`"modalities":{"input":["text","image"],"output":["text"]},"cost":{"input":0,"output":0}},` +
		`"minimax-m3.1-flash-free":{"name":"MiniMax","modalities":{"input":["text","image"],"output":["text"]},"cost":{"input":0,"output":0}}}}}`
	path := filepath.Join(dir, "models-dev-api.json")
	if err := os.WriteFile(path, []byte(catalogJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "opencode-enabled.json"), []byte(`{"version":"9.9.9","models":[{"ref":"opencode/free-vision"},{"ref":"opencode/minimax-m3.1-flash-free"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime := filepath.Join(dir, "opencode.exe")
	if err := os.WriteFile(runtime, []byte("MZ fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AIOVERLAY_OPENCODE_BIN", runtime)
	service, err := catalog.New(path)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func verdictFor(t *testing.T, models []domain.ModelMeta, ref string) domain.ModelMeta {
	t.Helper()
	for _, m := range models {
		if m.ProviderID+"/"+m.ModelID == ref {
			return m
		}
	}
	t.Fatalf("catalog has no %s", ref)
	return domain.ModelMeta{}
}

func attemptEvent(providerID, modelID string, outcome domain.Outcome, message string) domain.TraceEvent {
	return domain.TraceEvent{Kind: "attempt_end", Attempt: &domain.AttemptRef{ProviderID: providerID, ModelID: modelID}, Outcome: outcome, Message: message}
}

// The whole point of the observation layer: a real refusal must change what the next answer about
// readiness says. Before this, the runtime's "Model not found" lived only in a log file, and the app
// kept offering the retired model as ready.
func TestAttemptEvidenceChangesReadiness(t *testing.T) {
	a := studioTestApp(t)
	service := evidenceCatalog(t)
	a.catalog = service
	cfg := a.GetConfig()
	gateway := cfg.Providers[0] // the default embedded-OpenCode connection, keyless

	model := verdictFor(t, service.Annotated(cfg), "opencode/free-vision")
	if !model.Ready || model.Evidence != domain.ProvenanceHost {
		t.Fatalf("precondition: the published free model should be ready by host evidence: %+v", model)
	}

	a.recordAttemptEvidence(domain.ExecutionTrace{Events: []domain.TraceEvent{
		attemptEvent(gateway.ID, "opencode/free-vision", domain.OutcomeModelUnavailable,
			"provider_unavailable: OpenCode provider request failed - Model not found: opencode/free-vision"),
	}})
	model = verdictFor(t, service.Annotated(cfg), "opencode/free-vision")
	if model.Ready {
		t.Fatal("a model the runtime just refused is still presented as ready")
	}
	if model.Evidence != domain.ProvenanceObserved {
		t.Fatalf("evidence=%q, want observed", model.Evidence)
	}
	if len(model.Blockers) == 0 || model.Blockers[0].Source != domain.ProvenanceObserved {
		t.Fatalf("the refusal must be reported as the reason: %+v", model.Blockers)
	}
	if !strings.Contains(model.Blockers[0].Text, "Model not found") {
		t.Fatalf("the runtime's own words must survive: %q", model.Blockers[0].Text)
	}

	// The same connection answering later replaces the refusal: evidence is the last thing that
	// happened, not the worst.
	a.recordAttemptEvidence(domain.ExecutionTrace{Events: []domain.TraceEvent{
		attemptEvent(gateway.ID, "opencode/free-vision", domain.OutcomeSuccess, ""),
	}})
	if model = verdictFor(t, service.Annotated(cfg), "opencode/free-vision"); !model.Ready {
		t.Fatalf("a fresh answer must clear an older refusal: %+v", model.Blockers)
	}
	if model.Evidence != domain.ProvenanceObserved {
		t.Fatalf("a witnessed answer is the strongest evidence: %q", model.Evidence)
	}
}

// A timeout explains a request, not a model: it stays reportable history but may never retire one.
// Events that cannot be attributed to a configured connection must not reach the catalog at all.
func TestAttemptEvidenceIgnoresWhatItCannotAttribute(t *testing.T) {
	a := studioTestApp(t)
	service := evidenceCatalog(t)
	a.catalog = service
	cfg := a.GetConfig()
	gateway := cfg.Providers[0]

	a.recordAttemptEvidence(domain.ExecutionTrace{Events: []domain.TraceEvent{
		attemptEvent(gateway.ID, "opencode/free-vision", domain.OutcomeTimeout, "Request timed out"),
		attemptEvent("deleted-connection", "opencode/free-vision", domain.OutcomeModelUnavailable, "gone"),
		{Kind: "attempt_end", Outcome: domain.OutcomeModelUnavailable, Message: "no attempt reference"},
	}})
	if model := verdictFor(t, service.Annotated(cfg), "opencode/free-vision"); !model.Ready {
		t.Fatalf("a timeout must not block routing: %+v", model.Blockers)
	}
	if seen := service.Observations(); len(seen) != 1 || seen[0].ConnectionID != gateway.ID || seen[0].Outcome != domain.OutcomeTimeout {
		t.Fatalf("only the attributable, model-level outcome should be kept: %+v", seen)
	}
}

// The reference a trace carries and the reference the catalog stores must be the same string, or the
// evidence is written under one key and read under another - the same failure mode as the two
// catalogs, one layer up.
func TestObservedRefMatchesTheRouteReference(t *testing.T) {
	gateway := domain.ProviderConfig{ID: "oc", Kind: domain.ProviderEmbeddedOpenCode}
	if got := gateway.ObservedRef("opencode/free-vision"); got != "opencode/free-vision" {
		t.Fatalf("gateway ref=%q", got)
	}
	relay := domain.ProviderConfig{ID: "relay", Kind: domain.ProviderOpenAICompatible, CatalogProviderID: "deepseek"}
	if got := relay.ObservedRef("deepseek-chat"); got != "deepseek-chat" {
		t.Fatalf("non-gateway ref must stay unnamespaced: %q", got)
	}
	repointed := domain.ProviderConfig{ID: "oc", Kind: domain.ProviderEmbeddedOpenCode, OpenCodeProviderID: "anthropic"}
	if got := repointed.ObservedRef("anthropic/claude"); got != "anthropic/claude" {
		t.Fatalf("repointed gateway ref=%q", got)
	}
}
