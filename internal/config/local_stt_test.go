package config

import (
	"testing"

	"aioverlay/internal/domain"
)

func TestLocalSTTProviderValidationIsOfflineAndKeyless(t *testing.T) {
	cfg := Default()
	local := domain.ProviderConfig{
		ID: "local-stt", Label: "Local STT", Kind: domain.ProviderLocalSTT, Enabled: true,
		CredentialPool:  domain.CredentialPool{Policy: domain.CredentialSequential},
		RequestDefaults: defaultRequestDefaults(),
		LocalSTT: &domain.LocalSTTProviderConfig{
			HelperPath: "runtime/stt/aio-stt-helper.exe",
			ModelPath:  "runtime/stt/models/ggml-small.bin",
			ModelID:    "whisper-small-local",
		},
	}
	cfg.Providers = append(cfg.Providers, local)
	if err := Validate(cfg); err != nil {
		t.Fatalf("valid local STT provider rejected: %v", err)
	}

	withURL := cfg
	withURL.Providers = append([]domain.ProviderConfig(nil), cfg.Providers...)
	withURL.Providers[len(withURL.Providers)-1].BaseURL = "http://127.0.0.1:9999"
	if err := Validate(withURL); err == nil {
		t.Fatal("local STT accepted a network URL")
	}

	withKey := cfg
	withKey.Providers = append([]domain.ProviderConfig(nil), cfg.Providers...)
	withKey.Providers[len(withKey.Providers)-1].CredentialPool.Keys = []domain.Credential{{ID: "k", Secret: "secret", Enabled: true}}
	if err := Validate(withKey); err == nil {
		t.Fatal("local STT accepted a credential")
	}
}

func TestLocalSTTRejectsStaleRemoteConnectionFields(t *testing.T) {
	base := ProviderConfigForLocalSTTTest()
	for name, mutate := range map[string]func(*domain.ProviderConfig){
		"catalog":        func(provider *domain.ProviderConfig) { provider.CatalogProviderID = "remote" },
		"header":         func(provider *domain.ProviderConfig) { provider.Headers = map[string]string{"Authorization": "secret"} },
		"credential_env": func(provider *domain.ProviderConfig) { provider.CredentialEnv = []string{"REMOTE_KEY"} },
	} {
		t.Run(name, func(t *testing.T) {
			cfg := Default()
			provider := base
			mutate(&provider)
			cfg.Providers = append(cfg.Providers, provider)
			if err := Validate(cfg); err == nil {
				t.Fatal("local STT accepted stale remote connection state")
			}
		})
	}
}

func ProviderConfigForLocalSTTTest() domain.ProviderConfig {
	return domain.ProviderConfig{
		ID: "local-stt", Label: "Local STT", Kind: domain.ProviderLocalSTT, Enabled: true,
		CredentialPool:  domain.CredentialPool{Policy: domain.CredentialSequential},
		RequestDefaults: defaultRequestDefaults(),
		LocalSTT:        &domain.LocalSTTProviderConfig{HelperPath: "helper", ModelPath: "model", ModelID: "local-model"},
	}
}
