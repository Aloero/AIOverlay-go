package app

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"aioverlay/internal/domain"
)

// Universe must be exactly as honest as the scheduler: a model appears through the same apps the
// routes say can carry it, registration is a property of the config, and the free host-offered
// models show up even though the user registered nothing.
func TestUniverseAggregatesRoutesAndRegistration(t *testing.T) {
	a := studioTestApp(t)
	a.catalog = evidenceCatalog(t)

	u := a.Universe()
	if u.Snapshot != "9.9.9" {
		t.Fatalf("snapshot %q", u.Snapshot)
	}

	apps := map[string]bool{}
	for _, app := range u.Apps {
		apps[app.ID] = app.Available
	}
	if !apps[string(domain.AppDirect)] {
		t.Fatal("Direct API must always be available")
	}
	if !apps[string(domain.AppOpenCode)] {
		t.Fatalf("OpenCode is present in this fixture and must be available: %+v", u.Apps)
	}

	byRef := map[string]int{}
	for i, model := range u.Models {
		byRef[model.Ref] = i
	}
	minimax, ok := byRef["opencode/minimax-m3.1-flash-free"]
	if !ok {
		t.Fatalf("the published keyless model must appear: %+v", u.Models)
	}
	if !u.Models[minimax].Ready {
		t.Fatalf("a published offered model must be ready: %+v", u.Models[minimax])
	}
	if !containsApp(u.Models[minimax].Apps, string(domain.AppOpenCode)) {
		t.Fatalf("the offered model must be listed under opencode: %+v", u.Models[minimax].Apps)
	}
	if !u.Models[minimax].Free {
		t.Fatal("zero-cost offered model must be marked free")
	}

	byProvider := map[string]int{}
	for i, provider := range u.Providers {
		byProvider[provider.ID] = i
	}
	opencode, ok := byProvider["opencode"]
	if !ok {
		t.Fatalf("the default opencode connection must be listed: %+v", u.Providers)
	}
	if u.Providers[opencode].RegisteredVia != string(domain.AppOpenCode) {
		t.Fatalf("opencode registration must name its app: %+v", u.Providers[opencode])
	}
	// The relay in the default config has no key, so it is registered via "direct" but not keyed.
	if relay, ok := byProvider["openrouter"]; ok {
		if u.Providers[relay].Keyed {
			t.Fatal("an unconfigured relay must not report a key")
		}
	}
}

func containsApp(apps []string, want string) bool {
	for _, app := range apps {
		if app == want {
			return true
		}
	}
	return false
}

// The lazy-loading rule: opening the settings page must not launch a harness the user has not
// registered through. Only a registered Hermes connection may pay for `hermes --version`; the
// chip itself is allowed the cheap executable lookup, because the page offers registration.
func TestUniverseBootsHarnessesOnlyWhenRegistered(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	script := filepath.Join(dir, "hermes")
	body := "#!/bin/sh\necho called >> \"" + log + "\"\necho hermes-test 1.0\n"
	if runtime.GOOS == "windows" {
		script += ".cmd"
		body = "@echo off\r\necho called>>\"" + log + "\"\r\necho hermes-test 1.0\r\n"
	}
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AIOVERLAY_HERMES_BIN", script)

	a := studioTestApp(t)
	universe := a.Universe()
	if _, err := os.Stat(log); err == nil {
		t.Fatal("hermes was launched although no provider is registered through it")
	}
	for _, item := range universe.Apps {
		if item.ID == string(domain.AppHermes) && !item.Available {
			t.Fatalf("the PATH-resolvable harness must read available for registration: %+v", item)
		}
	}

	a.mu.Lock()
	a.cfg.Providers = append(a.cfg.Providers, domain.ProviderConfig{
		ID: "hermes-x", Label: "Hermes X", Kind: domain.ProviderEmbeddedHermes,
		App: domain.AppHermes, Enabled: true, BaseURL: "https://upstream.test/v1",
	})
	a.mu.Unlock()
	universe = a.Universe()
	if _, err := os.Stat(log); err != nil {
		t.Fatal("a registered hermes connection must version-probe its harness")
	}
	for _, item := range universe.Apps {
		if item.ID == string(domain.AppHermes) && item.Version == "" {
			t.Fatalf("the registered harness version must be shown: %+v", item)
		}
	}
}
