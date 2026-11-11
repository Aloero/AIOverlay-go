package domain

import "testing"

// These behaviours used to be `switch kind` blocks in the catalog, the engine and the app. They are
// the parts a wrong answer would hide: a model served under the wrong provider id, a pool that runs
// a connection with no credential at all when it needs one.
func TestPolicyTableCoversEveryShippedKind(t *testing.T) {
	for _, kind := range []ProviderKind{
		ProviderOpenAICompatible, ProviderGemini, ProviderTranscription,
		ProviderEmbeddedOpenCode, ProviderOpenAILive, ProviderLocalSTT,
	} {
		if !KnownProviderKind(kind) {
			t.Errorf("kind %s has no policy row", kind)
			continue
		}
		policy := Policy(kind)
		if policy.Media == (MediaPolicy{}) && !policy.AudioFromCfg {
			t.Errorf("kind %s declares no modality at all", kind)
		}
	}
	if KnownProviderKind(ProviderKind("typo")) {
		t.Error("an unknown kind must not look configured")
	}
	if len(ProviderRegistrations()) != len(policies) {
		t.Fatal("the registration list must cover the table")
	}
}

func TestCatalogProviderAndReferences(t *testing.T) {
	gateway := ProviderConfig{ID: "oc", Kind: ProviderEmbeddedOpenCode}
	if got := gateway.CatalogProvider(); got != "opencode" {
		t.Fatalf("a default gateway serves opencode, got %q", got)
	}
	// The field existing configs already use wins, and the reference stays namespaced because one
	// runtime fronts several upstreams.
	repointed := ProviderConfig{ID: "oc", Kind: ProviderEmbeddedOpenCode, OpenCodeProviderID: "anthropic"}
	if got := repointed.CatalogProvider(); got != "anthropic" {
		t.Fatalf("gateway provider override ignored: %q", got)
	}
	if got := repointed.ModelRef("anthropic", "claude"); got != "anthropic/claude" {
		t.Fatalf("gateway reference=%q", got)
	}
	if got := repointed.StripModelRef("anthropic/claude"); got != "claude" {
		t.Fatalf("strip=%q", got)
	}

	gemini := ProviderConfig{ID: "gemini-conn", Kind: ProviderGemini}
	if got := gemini.CatalogProvider(); got != "google" {
		t.Fatalf("gemini models belong to google, got %q", got)
	}
	if got := gemini.ModelRef("google", "flash"); got != "flash" {
		t.Fatalf("a non-gateway reference must not be namespaced: %q", got)
	}
	// A connection may always override the kind default; otherwise it is identified by its own id.
	custom := ProviderConfig{ID: "relay", Kind: ProviderOpenAICompatible, CatalogProviderID: "deepseek"}
	if got := custom.CatalogProvider(); got != "deepseek" {
		t.Fatalf("override ignored: %q", got)
	}
	if got := (ProviderConfig{ID: "relay", Kind: ProviderOpenAICompatible}).CatalogProvider(); got != "relay" {
		t.Fatalf("unknown family must fall back to the connection id: %q", got)
	}
}

func TestAnonymousCredentials(t *testing.T) {
	// The bundled pool is the only keyless gateway upstream.
	keyless, ok := (ProviderConfig{ID: "oc", Kind: ProviderEmbeddedOpenCode}).AnonymousCredential()
	if !ok || keyless.ID != "keyless" || !keyless.Enabled {
		t.Fatalf("the default gateway must run keyless: %+v ok=%v", keyless, ok)
	}
	// Repointed at someone else's upstream, the same connection needs that upstream's key.
	if _, ok := (ProviderConfig{ID: "oc", Kind: ProviderEmbeddedOpenCode, OpenCodeProviderID: "openrouter"}).AnonymousCredential(); ok {
		t.Fatal("a gateway on a third-party upstream cannot be keyless")
	}
	local, ok := (ProviderConfig{ID: "whisper", Kind: ProviderLocalSTT}).AnonymousCredential()
	if !ok || local.ID != "local" {
		t.Fatalf("a local runtime runs under its own synthetic key: %+v ok=%v", local, ok)
	}
	for _, cfg := range []ProviderConfig{
		{ID: "openai", Kind: ProviderOpenAICompatible},
		{ID: "live", Kind: ProviderOpenAILive},
		{ID: "stt", Kind: ProviderTranscription},
	} {
		if _, ok := cfg.AnonymousCredential(); ok {
			t.Errorf("%s must require a real credential", cfg.Kind)
		}
	}
}

func TestEndpointShapeFacts(t *testing.T) {
	// Only a hosted kind may omit a base URL, and only the local runtime must not have one.
	for _, kind := range []ProviderKind{ProviderEmbeddedOpenCode, ProviderLocalSTT} {
		if !Policy(kind).Hosted {
			t.Errorf("%s is hosted and must not require a base URL", kind)
		}
	}
	for _, kind := range []ProviderKind{ProviderOpenAICompatible, ProviderGemini, ProviderTranscription, ProviderOpenAILive} {
		if Policy(kind).Hosted {
			t.Errorf("%s needs an endpoint in its config", kind)
		}
	}
	if !Policy(ProviderOpenAILive).RealtimeProtocol || Policy(ProviderOpenAICompatible).RealtimeProtocol {
		t.Error("the persistent-session rule must apply to Live only")
	}
	for _, kind := range []ProviderKind{ProviderOpenAILive, ProviderLocalSTT} {
		if Policy(kind).ProbeBlockedReason == "" {
			t.Errorf("%s cannot be tested by a one-shot probe and must say why", kind)
		}
	}
	if Policy(ProviderLocalSTT).LocalRuntime != true || Policy(ProviderEmbeddedOpenCode).LocalRuntime {
		t.Error("the private runtime section belongs to local STT only")
	}
	if !Policy(ProviderTranscription).SingleBody || Policy(ProviderOpenAICompatible).SingleBody {
		t.Error("delivery shape decides whether a first-token deadline is meaningful")
	}
}
