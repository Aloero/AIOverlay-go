package config

import (
	"testing"

	"aioverlay/internal/domain"
)

func TestOpenAILiveProviderRequiresCredentialFreeWSS(t *testing.T) {
	base := Default()
	live := domain.ProviderConfig{
		ID: "live", Label: "Live", Kind: domain.ProviderOpenAILive, Enabled: true,
		BaseURL:        "wss://api.openai.com/v1/live/sessions",
		CredentialPool: domain.CredentialPool{Policy: domain.CredentialSequential},
		Realtime:       &domain.RealtimeProviderConfig{ResponsesModel: "gpt-5.6", Instructions: "Answer briefly."},
	}
	base.Providers = append(base.Providers, live)
	if err := Validate(base); err != nil {
		t.Fatalf("valid Live provider rejected: %v", err)
	}
	for _, invalid := range []string{
		"https://api.openai.com/v1/live/sessions",
		"wss://user:secret@api.openai.com/v1/live/sessions",
		"wss://api.openai.com/v1/live/sessions?token=secret",
	} {
		cfg := base
		cfg.Providers = append([]domain.ProviderConfig(nil), base.Providers...)
		cfg.Providers[len(cfg.Providers)-1].BaseURL = invalid
		if err := Validate(cfg); err == nil {
			t.Fatalf("invalid Live endpoint accepted: %s", invalid)
		}
	}
}

func TestOpenAILiveCannotBeUsedAsOrdinaryModelPool(t *testing.T) {
	cfg := Default()
	cfg.Providers = append(cfg.Providers, domain.ProviderConfig{
		ID: "live", Label: "Live", Kind: domain.ProviderOpenAILive, Enabled: true,
		BaseURL:        "wss://api.openai.com/v1/live/sessions",
		CredentialPool: domain.CredentialPool{Policy: domain.CredentialSequential},
	})
	pool := cfg.Pipelines[0].Nodes[1].ModelPool
	pool.Models = []domain.ModelSlot{{ID: "live", ProviderID: "live", ModelID: "gpt-live-1", Enabled: true, Priority: 100, QualityScore: 100}}
	if err := Validate(cfg); err == nil {
		t.Fatal("persistent Live provider was accepted as a stateless model-pool route")
	}
}
