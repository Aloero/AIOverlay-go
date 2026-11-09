package config

import (
	"strings"
	"testing"

	"aioverlay/internal/domain"
)

func TestProviderRejectsMalformedBaseURL(t *testing.T) {
	for _, provider := range []struct {
		name   string
		kind   domain.ProviderKind
		scheme string
	}{
		{name: "compatible", kind: domain.ProviderOpenAICompatible, scheme: "https"},
		{name: "live", kind: domain.ProviderOpenAILive, scheme: "wss"},
	} {
		for _, endpoint := range []struct {
			name   string
			suffix string
		}{
			{name: "invalid_escape", suffix: "://example.invalid/%zz"},
			{name: "invalid_port", suffix: "://example.invalid:not-a-port"},
			{name: "invalid_host", suffix: "://[broken"},
			{name: "control_character", suffix: "://example.invalid/\n"},
		} {
			t.Run(provider.name+"/"+endpoint.name, func(t *testing.T) {
				cfg := Default()
				cfg.Providers = append(cfg.Providers, domain.ProviderConfig{
					ID: "malformed-url", Kind: provider.kind, Enabled: true,
					BaseURL:        provider.scheme + endpoint.suffix,
					CredentialPool: domain.CredentialPool{Policy: domain.CredentialSequential},
				})
				if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "invalid base URL") {
					t.Fatalf("malformed provider URL must return a validation error: %v", err)
				}
			})
		}
	}
}
