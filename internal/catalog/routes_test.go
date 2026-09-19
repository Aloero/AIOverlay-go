package catalog

import (
	"path/filepath"
	"strings"
	"testing"

	"aioverlay/internal/config"
	"aioverlay/internal/domain"
)

func TestMissingCostIsNotFree(t *testing.T) {
	for _, cost := range []string{"", `,"cost":{}`, `,"cost":{"input":0}`, `,"cost":{"output":0}`} {
		models, err := parse(strings.NewReader(`{"p":{"name":"P","models":{"m":{"name":"Unknown cost"` + cost + `}}}}`))
		if err != nil || len(models) != 1 || models[0].Free {
			t.Fatal("missing/incomplete price incorrectly marked FREE", err)
		}
	}
}
func TestOpenCodeDisabledAndPaidModelsNotReadyWithoutKey(t *testing.T) {
	s, err := New(filepath.Join("..", "..", "runtime", "catalog", "models-dev-api.json"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	for _, r := range s.Routes(cfg) {
		if r.Transport == "Via OpenCode" && !r.Model.Free && r.Ready {
			t.Fatal("paid model ready without key")
		}
	}
	cfg.Providers[0].Enabled = false
	for _, r := range s.Routes(cfg) {
		if r.ConfigID == cfg.Providers[0].ID && r.Ready {
			t.Fatal("disabled keyless route ready")
		}
	}
}

func TestCatalogDoesNotTreatResourceNamesOrPathsAsAPIKeys(t *testing.T) {
	env := credentialEnvs([]string{"AZURE_RESOURCE_NAME", "AZURE_API_KEY", "GOOGLE_APPLICATION_CREDENTIALS", "AWS_ACCESS_KEY_ID", "HF_TOKEN", "AZURE_API_KEY"})
	if len(env) != 2 || env[0] != "AZURE_API_KEY" || env[1] != "HF_TOKEN" {
		t.Fatalf("unsafe inferred credential fields: %v", env)
	}
}

func TestLocalAndLiveProvidersNeverBecomeModelsDevRoutes(t *testing.T) {
	service, err := New(filepath.Join("..", "..", "runtime", "catalog", "models-dev-api.json"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Providers = []domain.ProviderConfig{
		{ID: "openrouter", Kind: domain.ProviderLocalSTT, Enabled: true, LocalSTT: &domain.LocalSTTProviderConfig{HelperPath: "helper", ModelPath: "model", ModelID: "local"}},
		{ID: "google", Kind: domain.ProviderOpenAILive, Enabled: true, BaseURL: "wss://example.test/v1/live/sessions"},
	}
	for _, route := range service.Routes(cfg) {
		if route.ConfigID == "openrouter" || route.ConfigID == "google" {
			t.Fatalf("non-catalog transport became Models.dev route: %+v", route)
		}
	}
}
