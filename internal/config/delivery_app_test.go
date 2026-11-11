package config

import (
	"encoding/json"
	"testing"

	"aioverlay/internal/domain"
)

func TestMigrateSixNamesDeliveryApps(t *testing.T) {
	cfg := Default()
	cfg.Version = 6
	for i := range cfg.Providers {
		cfg.Providers[i].App = ""
	}
	if err := Migrate(&cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Version != domain.ConfigVersion {
		t.Fatalf("version %d", cfg.Version)
	}
	for _, p := range cfg.Providers {
		want := domain.AppForKind(p.Kind)
		if p.App != want {
			t.Fatalf("provider %s: app %s, want %s", p.ID, p.App, want)
		}
	}
}

func TestDeliveryAppMustMatchKind(t *testing.T) {
	cfg := Default()
	for i := range cfg.Providers {
		if cfg.Providers[i].Kind == domain.ProviderOpenAICompatible {
			cfg.Providers[i].App = domain.AppOpenCode
			if Validate(cfg) == nil {
				t.Fatal("app/protocol contradiction accepted")
			}
			return
		}
	}
	t.Fatal("fixture has no direct-protocol provider")
}

func TestHermesProviderIsValid(t *testing.T) {
	cfg := Default()
	cfg.Providers = append(cfg.Providers, domain.ProviderConfig{
		ID: "hermes-main", Label: "Hermes", Kind: domain.ProviderEmbeddedHermes,
		App: domain.AppHermes, Enabled: true, BaseURL: "https://upstream.test/v1",
		CredentialPool:  domain.CredentialPool{Policy: domain.CredentialRoundRobin, MaxKeysPerRequest: 1, RateLimitCooldownS: 60},
		RequestDefaults: defaultRequestDefaults(),
	})
	if err := Validate(cfg); err != nil {
		t.Fatalf("hermes provider rejected: %v", err)
	}
	policy := domain.Policy(domain.ProviderEmbeddedHermes)
	// The harness is the multimodal connector: it carries images (--image) but not audio.
	if !policy.Hosted || !policy.SingleBody || !policy.Media.Image || policy.Media.Audio || policy.TransportLabel != "Via Hermes" {
		t.Fatalf("unexpected hermes policy: %+v", policy)
	}
	if got := (domain.ProviderConfig{Kind: domain.ProviderEmbeddedHermes}).DeliveryApp(); got != domain.AppHermes {
		t.Fatalf("derived app %s", got)
	}
}

func TestHermesRequiresUpstreamURL(t *testing.T) {
	cfg := Default()
	cfg.Providers = append(cfg.Providers, domain.ProviderConfig{
		ID: "hermes-url", Label: "Hermes", Kind: domain.ProviderEmbeddedHermes,
		App: domain.AppHermes, Enabled: true,
		CredentialPool:  domain.CredentialPool{Policy: domain.CredentialRoundRobin, MaxKeysPerRequest: 1, RateLimitCooldownS: 60},
		RequestDefaults: defaultRequestDefaults(),
	})
	if Validate(cfg) == nil {
		t.Fatal("hermes connection without base URL accepted")
	}
}

func TestSlotAppParamsRoundTrip(t *testing.T) {
	cfg := Default()
	slot := &cfg.Pipelines[0].Nodes[1].ModelPool.Models[0]
	slot.AppParams = map[string]json.RawMessage{
		"reasoning": json.RawMessage(`"high"`),
		"extra":     json.RawMessage(`{"n":1}`),
	}
	s := NewStore(t.TempDir() + "/config.json")
	if err := s.Save(cfg); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	// RawMessage keeps the encoder's formatting, so equality is checked semantically.
	if len(got.Pipelines[0].Nodes[1].ModelPool.Models[0].AppParams) != 2 {
		t.Fatalf("app params lost: %v", got.Pipelines[0].Nodes[1].ModelPool.Models[0].AppParams)
	}
	var effort string
	if err := json.Unmarshal(got.Pipelines[0].Nodes[1].ModelPool.Models[0].AppParams["reasoning"], &effort); err != nil || effort != "high" {
		t.Fatalf("reasoning lost: %q %v", effort, err)
	}
}
