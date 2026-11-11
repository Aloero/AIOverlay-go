package providers

import (
	"context"
	"strings"
	"testing"

	"aioverlay/internal/domain"
	"aioverlay/internal/modelhost"
)

// A capability statement without an author is how the app came to believe a model could see a
// screenshot it never could. The report must therefore name the layer that declared each modality
// set and, when a host stands behind the connection, which host and version.
func TestAdapterCapabilitiesNameTheirAuthorAndHost(t *testing.T) {
	host := &fakeHost{endpoint: modelhost.Endpoint{BaseURL: "http://127.0.0.1:1"}}
	adapter := newEmbeddedOpenCode(domain.ProviderConfig{ID: "oc", Kind: domain.ProviderEmbeddedOpenCode, Enabled: true}, Deps{
		HostFactory: func(domain.ProviderConfig) (modelhost.Host, error) { return host, nil },
	})

	capabilities := adapterCapabilities(adapter.cfg, adapter)
	if capabilities.MediaSource != domain.ProvenanceAdapter {
		t.Fatalf("an adapter that declares its media must be reported as adapter-sourced, got %q", capabilities.MediaSource)
	}
	// The fake host is a plain host: the OpenCode adapter answers vision from its policy row, so
	// the claim must carry a real author either way - never ProvenanceUnknown.
	if capabilities.MediaSource == domain.ProvenanceUnknown {
		t.Fatal("media provenance is unreported")
	}
	if capabilities.Host != "fake" || capabilities.HostVersion != "test" {
		t.Fatalf("host identity=%q/%q, want fake/test", capabilities.Host, capabilities.HostVersion)
	}

	// A connection with no host behind it (plain remote endpoint) must not invent one.
	remote := NewOpenAI(domain.ProviderConfig{ID: "relay", Kind: domain.ProviderOpenAICompatible, BaseURL: "https://relay.example/v1"})
	remoteCapabilities := adapterCapabilities(remote.cfg, remote)
	if remoteCapabilities.Host != "" || remoteCapabilities.HostVersion != "" {
		t.Fatalf("a bare remote endpoint has no host: %+v", remoteCapabilities)
	}
	// The transport itself answers for the media of every concrete adapter: nothing shipped is
	// inferred from config alone.
	if remoteCapabilities.MediaSource != domain.ProvenanceAdapter {
		t.Fatalf("openai-compatible media source=%q, want adapter", remoteCapabilities.MediaSource)
	}
}

// capabilityProvenance falls back to "config" only when nothing declared anything - the case for a
// provider registered without its own capability report.
func TestCapabilityProvenanceFallback(t *testing.T) {
	plain := newEmbeddedOpenCode(domain.ProviderConfig{ID: "oc", Kind: domain.ProviderEmbeddedOpenCode}, Deps{
		HostFactory: func(domain.ProviderConfig) (modelhost.Host, error) { return &fakeHost{}, nil },
	})
	if got := capabilityProvenance(fakeNoCapabilities{}, nil); got != domain.ProvenanceConfig {
		t.Fatalf("no declaration, no reporter: %q, want config", got)
	}
	if got := capabilityProvenance(plain, []MediaCapabilities{{Image: true}}); got != domain.ProvenanceAdapter {
		t.Fatalf("explicit declaration: %q, want adapter", got)
	}
	if got := capabilityProvenance(plain, nil); got != domain.ProvenanceAdapter {
		t.Fatalf("adapter reporting its own capabilities: %q, want adapter", got)
	}
}

type fakeNoCapabilities struct{}

func (fakeNoCapabilities) ID() string { return "none" }
func (fakeNoCapabilities) Stream(context.Context, Request, string) (<-chan Event, <-chan error) {
	ch := make(chan Event)
	close(ch)
	errs := make(chan error)
	close(errs)
	return ch, errs
}

// The quota incident on the VM: the runtime refused with "Free usage limit reached" while the overlay
// showed only a timeout. The provider's own words must survive normalization into RequestError.Detail
// - bounded, single-line, never a raw body.
func TestOpenCodeErrorKeepsProviderReason(t *testing.T) {
	err := openCodeError([]byte(`{"name":"APIError","data":{"statusCode":500,"message":"Free usage limit reached. Free models work in OpenCode (https://opencode.ai); subscribe to OpenCode Go to keep going: https://opencode.ai/go"}}`))
	if !strings.Contains(err.Detail, "Free usage limit reached") {
		t.Fatalf("the quota refusal must reach Detail: %q", err.Detail)
	}
	if !strings.Contains(err.Error(), "Free usage limit reached") {
		t.Fatalf("Error() must carry the reason too: %q", err.Error())
	}
	if !strings.Contains(err.Message, "OpenCode provider request failed") {
		t.Fatalf("Message stays the stable sentence: %q", err.Message)
	}

	// Long reasons are truncated by runes, never mid-UTF8, and JSON fragments do not leak.
	long := openCodeError([]byte(`{"name":"APIError","data":{"statusCode":400,"message":"` + strings.Repeat("модель ", 60) + `{"secret":"sk-hidden"}"}}`))
	if len([]rune(long.Detail)) > 201 {
		t.Fatalf("detail not bounded: %d runes", len([]rune(long.Detail)))
	}
	if strings.Contains(long.Detail, "sk-") || strings.Contains(long.Detail, "{") {
		t.Fatalf("body or key fragment leaked: %q", long.Detail)
	}

	// No message in the envelope: Detail stays empty and Error() is unchanged.
	quiet := openCodeError([]byte(`{"name":"ProviderAuthError","data":{}}`))
	if quiet.Detail != "" {
		t.Fatalf("nothing stated, nothing quoted: %q", quiet.Detail)
	}
}
