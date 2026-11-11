package catalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aioverlay/internal/domain"
)

// fixture builds a catalog, a published-offerings snapshot and (optionally) an installed runtime in
// one temp directory, so every assertion below is about evidence rather than about whatever the
// bundled data happens to contain today.
type fixture struct {
	service *Service
	now     time.Time
}

func newFixture(t *testing.T, offerings bool, runtimeInstalled bool) fixture {
	t.Helper()
	dir := t.TempDir()
	catalog := `{"opencode":{"name":"OpenCode Zen","models":{` +
		`"free-vision":{"name":"Free Vision","modalities":{"input":["text","image"],"output":["text"]},"cost":{"input":0,"output":0},"limit":{"context":1000,"output":100}},` +
		`"paid-text":{"name":"Paid Text","modalities":{"input":["text"],"output":["text"]},"cost":{"input":3,"output":12}}` +
		`}},"other":{"name":"Other","models":{"model":{"name":"Model","cost":{"input":1,"output":2}}}}}`
	path := filepath.Join(dir, "models-dev-api.json")
	if err := os.WriteFile(path, []byte(catalog), 0o600); err != nil {
		t.Fatal(err)
	}
	if offerings {
		enabled := `{"version":"9.9.9","models":[{"ref":"opencode/free-vision"},{"ref":"opencode/paid-text"}]}`
		if err := os.WriteFile(filepath.Join(dir, "opencode-enabled.json"), []byte(enabled), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	runtime := filepath.Join(dir, "..", "opencode", "opencode.exe")
	if runtimeInstalled {
		if err := os.MkdirAll(filepath.Dir(runtime), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(runtime, []byte("MZ not really a binary"), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("AIOVERLAY_OPENCODE_BIN", runtime)
	} else {
		// t.Setenv lasts for the whole test, so a fixture that wants "not installed" must clear the
		// override a previous fixture in the same test left behind.
		t.Setenv("AIOVERLAY_OPENCODE_BIN", "")
	}
	service, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)
	frozen := at
	service.now = func() time.Time { return frozen }
	return fixture{service: service, now: at}
}

func (f fixture) routes(t *testing.T, providers ...domain.ProviderConfig) []Route {
	t.Helper()
	byRef := map[string]Route{}
	for _, route := range f.service.routes(domain.Config{Providers: providers}, f.now) {
		byRef[route.ConfigID+"/"+route.ModelID] = route
	}
	return mapValues(byRef)
}

func mapValues(in map[string]Route) []Route {
	out := make([]Route, 0, len(in))
	for _, route := range in {
		out = append(out, route)
	}
	return out
}

func gateway(id string, keys ...domain.Credential) domain.ProviderConfig {
	return domain.ProviderConfig{ID: id, Kind: domain.ProviderEmbeddedOpenCode, Enabled: true, CredentialPool: domain.CredentialPool{Keys: keys}}
}

// wantRoute fails the test when the fixture did not produce the route it is about to assert on, so a
// broken fixture cannot pass by checking nothing.
func wantRoute(t *testing.T, routes []Route, modelID string) Route {
	t.Helper()
	for _, route := range routes {
		if route.ModelID == modelID {
			return route
		}
	}
	t.Fatalf("no route for %s in %+v", modelID, routes)
	return Route{}
}

func blocker(route Route, code string) (domain.Blocker, bool) {
	for _, candidate := range route.Verdict.Blockers {
		if candidate.Code == code {
			return candidate, true
		}
	}
	return domain.Blocker{}, false
}

// A keyless gateway route is ready because the *host* published the model, not because the app
// decided it was free. That distinction is the whole point: the earlier bug was a ready flag whose
// author was the wish list itself.
func TestKeylessReadinessNamesTheHost(t *testing.T) {
	routes := newFixture(t, true, true).routes(t, gateway("oc"))
	route := wantRoute(t, routes, "opencode/free-vision")
	if !route.Ready() {
		t.Fatalf("the published free model must be usable with no key: %+v", routes)
	}
	if route.Verdict.Evidence != domain.ProvenanceHost {
		t.Fatalf("evidence=%q, want host", route.Verdict.Evidence)
	}

	// Without the snapshot, the same model has no author for its readiness, so it is not ready -
	// and the blocker says which snapshot was consulted.
	unpublished := wantRoute(t, newFixture(t, false, true).routes(t, gateway("oc")), "opencode/free-vision")
	if unpublished.Ready() {
		t.Fatal("an unpublished model is not ready just because the catalog marks it free")
	}
	missing, ok := blocker(unpublished, domain.BlockNotOffered)
	if !ok || missing.Source != domain.ProvenanceHost {
		t.Fatalf("want a host-sourced not_offered blocker, got %+v", unpublished.Verdict.Blockers)
	}
	if !contains(missing.Text, "no snapshot") {
		t.Fatalf("the blocker must name the snapshot it consulted: %q", missing.Text)
	}
	// With a snapshot present, the blocker names its version: a claim about what the host publishes is
	// only meaningful together with the moment it was taken.
	paid := wantRoute(t, newFixture(t, true, true).routes(t, gateway("oc")), "opencode/paid-text")
	if missing, ok := blocker(paid, domain.BlockNotOffered); !ok || !contains(missing.Text, "9.9.9") {
		t.Fatalf("the blocker must name the snapshot version: %+v", paid.Verdict.Blockers)
	}
}

// The runtime binary being absent is a host fact and must be reported as such: before this, the only
// answer the user got was a timeout from an attempt that never reached a process.
func TestMissingRuntimeIsReportedByTheHostLayer(t *testing.T) {
	for _, route := range newFixture(t, true, false).routes(t, gateway("oc")) {
		if !contains(route.ModelID, "gateway/") {
			continue
		}
		if route.Ready() {
			t.Fatalf("a route with no runtime is not usable: %+v", route)
		}
		missing, ok := blocker(route, domain.BlockRuntimeMissing)
		if !ok || missing.Source != domain.ProvenanceHost {
			t.Fatalf("want a host-sourced runtime_missing blocker, got %+v", route.Verdict.Blockers)
		}
	}
}

// A watched refusal outranks a published offering, expires, and never survives a catalog replace.
func TestObservedRefusalOutranksEveryFile(t *testing.T) {
	f := newFixture(t, true, true)
	if route := wantRoute(t, f.routes(t, gateway("oc")), "opencode/free-vision"); !route.Ready() {
		t.Fatalf("precondition: the published model should start ready: %+v", route.Verdict.Blockers)
	}
	f.service.Observe("oc", "opencode/free-vision", domain.OutcomeModelUnavailable, "ProviderModelNotFoundError: Model not found")
	after := f.routes(t, gateway("oc"))
	for _, route := range after {
		if route.ModelID != "opencode/free-vision" {
			continue
		}
		if route.Ready() {
			t.Fatal("a model the host refused seconds ago must not be offered as ready")
		}
		refused, ok := blocker(route, domain.BlockModelRefused)
		if !ok || refused.Source != domain.ProvenanceObserved {
			t.Fatalf("want an observed model_refused blocker, got %+v", route.Verdict.Blockers)
		}
		if route.Verdict.Evidence != domain.ProvenanceObserved {
			t.Fatalf("evidence=%q, want observed", route.Verdict.Evidence)
		}
		if !contains(route.Verdict.Reason(), "Model not found") {
			t.Fatalf("Reason() must surface the watched text, got %q", route.Verdict.Reason())
		}
	}

	// The same evidence, seen after the TTL, informs but no longer blocks: the app cannot tell a
	// retired model from one that was briefly overloaded.
	f.now = f.now.Add(observationTTL + time.Minute)
	for _, route := range f.routes(t, gateway("oc")) {
		if route.ModelID == "opencode/free-vision" && !route.Ready() {
			t.Fatalf("a stale observation must stop gating routing: %+v", route.Verdict.Blockers)
		}
	}
	if seen := f.service.Observations(); len(seen) != 1 || seen[0].ConnectionID != "oc" {
		t.Fatalf("the observation must remain reportable as history: %+v", seen)
	}

	// A replaced catalog list is a different world; the old refusal described the removed data.
	f.service.replace([]domain.ModelMeta{{ProviderID: "gateway", ModelID: "free-vision"}})
	if seen := f.service.Observations(); len(seen) != 0 {
		t.Fatalf("a catalog replace must invalidate watched refusals: %+v", seen)
	}
}

// Quota and rate refusals are model-blockers too, but distinguishable ones: the earlier incident was
// exactly "Free usage limit reached" arriving as an anonymous timeout.
func TestQuotaRefusalIsClassified(t *testing.T) {
	f := newFixture(t, true, true)
	f.service.Observe("oc", "opencode/free-vision", domain.OutcomeProviderUnavailable, "AI_APICallError: Free usage limit reached. subscribe to keep going")
	for _, route := range f.routes(t, gateway("oc")) {
		if route.ModelID != "opencode/free-vision" {
			continue
		}
		limited, ok := blocker(route, domain.BlockRateLimited)
		if !ok || limited.Source != domain.ProvenanceObserved {
			t.Fatalf("want a rate_limited blocker, got %+v", route.Verdict.Blockers)
		}
	}
	// A timeout says nothing about the model - it may have died anywhere on the path.
	fresh := newFixture(t, true, true)
	fresh.service.Observe("oc", "opencode/free-vision", domain.OutcomeTimeout, "context deadline exceeded")
	for _, route := range fresh.routes(t, gateway("oc")) {
		if route.ModelID == "opencode/free-vision" && !route.Ready() {
			t.Fatalf("a timeout must not retire a model: %+v", route.Verdict.Blockers)
		}
	}
}

// Two connections can serve the same catalog model with different answers. The union must be ready if
// any route is ready, and must still show every distinct reason when none is.
func TestAnnotatedMergesVerdictsAcrossConnections(t *testing.T) {
	f := newFixture(t, true, true)
	good := gateway("oc")
	bad := gateway("oc-offline")
	bad.Enabled = false
	models := f.service.Annotated(domain.Config{Providers: []domain.ProviderConfig{good, bad}})
	for _, m := range models {
		switch m.ProviderID + "/" + m.ModelID {
		case "opencode/free-vision":
			if !m.Ready || m.Status != "CONFIGURED" || m.Evidence != domain.ProvenanceHost {
				t.Fatalf("one usable route makes the model usable: %+v", m)
			}
		case "other/model":
			if m.Ready || m.Status != "NOT CONFIGURED" {
				t.Fatalf("no connection serves this provider: %+v", m)
			}
			if len(m.Blockers) == 0 || m.Blockers[0].Source != domain.ProvenanceConfig {
				t.Fatalf("an unconfigured model needs a config-sourced reason: %+v", m.Blockers)
			}
		}
	}

	// With the working connection also refused by the host, both reasons survive and the strongest
	// source labels the verdict.
	f.service.Observe("oc", "opencode/free-vision", domain.OutcomeQuotaExceeded, "quota exhausted")
	for _, m := range f.service.Annotated(domain.Config{Providers: []domain.ProviderConfig{good, bad}}) {
		if m.ProviderID+"/"+m.ModelID != "opencode/free-vision" {
			continue
		}
		if m.Ready {
			t.Fatal("both routes are blocked, so the model is not ready")
		}
		if m.Evidence != domain.ProvenanceObserved {
			t.Fatalf("evidence=%q, want observed", m.Evidence)
		}
		codes := map[string]bool{}
		for _, blocker := range m.Blockers {
			codes[blocker.Code] = true
		}
		if !codes[domain.BlockDisabled] || !codes[domain.BlockRateLimited] {
			t.Fatalf("both distinct reasons must be reported: %+v", m.Blockers)
		}
	}
}

func contains(haystack, needle string) bool { return strings.Contains(haystack, needle) }

// ConnectionVerdicts is the report's per-connection answer. It must cover kinds the directory does
// not list at all (a local runtime, a Live session) - the model-level merge has nothing to say about
// those, and a report that omitted them would claim there was nothing to check.
func TestConnectionVerdictsCoverEveryProvider(t *testing.T) {
	f := newFixture(t, true, true)
	local := domain.ProviderConfig{ID: "whisper", Kind: domain.ProviderLocalSTT, Enabled: true}
	relay := domain.ProviderConfig{ID: "relay", Kind: domain.ProviderOpenAICompatible, Enabled: true}
	verdicts := f.service.ConnectionVerdicts(domain.Config{Providers: []domain.ProviderConfig{
		gateway("oc"), local, relay,
	}})
	if verdict, ok := verdicts["oc"]; !ok || !verdict.Ready {
		t.Fatalf("a keyless gateway with runtime present is usable: %+v", verdict)
	}
	if verdict, ok := verdicts["whisper"]; !ok || !verdict.Ready {
		t.Fatalf("the local runtime needs no key or endpoint: %+v (ok=%v)", verdict, ok)
	}
	verdict, ok := verdicts["relay"]
	if !ok || verdict.Ready {
		t.Fatalf("a relay with no URL and no key is not usable: %+v", verdict)
	}
	codes := map[string]bool{}
	for _, blocker := range verdict.Blockers {
		codes[blocker.Code] = true
	}
	if !codes[domain.BlockNoEndpoint] || !codes[domain.BlockNoKey] {
		t.Fatalf("both config reasons must be present: %+v", verdict.Blockers)
	}
}

// VerdictsFor answers only about the refs it is asked - and says "no_route" out loud for a ref no
// connection serves, instead of leaving the pool editor with a model that silently never runs.
func TestVerdictsForNamesUnreachablePoolRefs(t *testing.T) {
	f := newFixture(t, true, true)
	verdicts := f.service.VerdictsFor(domain.Config{Providers: []domain.ProviderConfig{gateway("oc")}},
		[]string{"opencode/free-vision", "ghost/model", "opencode/paid-text"})
	if verdict := verdicts["opencode/free-vision"]; !verdict.Ready {
		t.Fatalf("published free model must be ready: %+v", verdict.Blockers)
	}
	ghost, ok := verdicts["ghost/model"]
	if !ok || ghost.Ready {
		t.Fatalf("an unrouted ref must be reported, not skipped: %+v", ghost)
	}
	if code := ghost.Blockers[0].Code; code != domain.BlockNoRoute {
		t.Fatalf("code=%q, want %q", code, domain.BlockNoRoute)
	}
	// A ready route and a blocked route for the same ref resolve to ready: one working road is
	// enough, exactly as the scheduler treats it.
	both := f.service.VerdictsFor(domain.Config{Providers: []domain.ProviderConfig{
		gateway("oc"), gateway("oc-off"),
	}}, []string{"opencode/free-vision"})
	if !both["opencode/free-vision"].Ready {
		t.Fatal("a second disabled connection must not retract a working route")
	}
}
