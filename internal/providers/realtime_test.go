package providers

import (
	"aioverlay/internal/config"
	"aioverlay/internal/domain"
	"context"
	"fmt"
	"testing"
)

type realtimeCapabilityFixture struct{}

type realtimeSessionFixture struct {
	capabilities RealtimeCapabilities
}

func (s *realtimeSessionFixture) Capabilities() RealtimeCapabilities { return s.capabilities }
func (*realtimeSessionFixture) Close() error                         { return nil }

func (*realtimeCapabilityFixture) ID() string { return "fixture" }
func (*realtimeCapabilityFixture) Stream(context.Context, Request, string) (<-chan Event, <-chan error) {
	events := make(chan Event)
	errors := make(chan error)
	close(events)
	close(errors)
	return events, errors
}
func (*realtimeCapabilityFixture) RealtimeCapabilities() RealtimeCapabilities {
	capabilities := UnsupportedRealtime("")
	capabilities.Available = true
	capabilities.Operations[RealtimeAppendAudio] = true
	capabilities.Operations[RealtimeRequestResponse] = true
	capabilities.Operations[RealtimeTextOutput] = true
	return capabilities
}
func (p *realtimeCapabilityFixture) OpenRealtime(context.Context, RealtimeOpenRequest) (RealtimeSession, error) {
	return &realtimeSessionFixture{capabilities: p.RealtimeCapabilities()}, nil
}

func TestCurrentHTTPAdaptersDoNotClaimPersistentRealtime(t *testing.T) {
	cfg := config.Default()
	registry, err := NewRegistry(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(registry.Close)

	for _, capabilities := range registry.Capabilities(cfg) {
		if capabilities.Realtime.Available {
			t.Fatalf("%s unexpectedly claims realtime readiness", capabilities.ProviderID)
		}
		for _, operation := range realtimeOperations {
			if capabilities.Realtime.Supports(operation) {
				t.Fatalf("%s unexpectedly supports %s", capabilities.ProviderID, operation)
			}
		}
	}
}

func TestRealtimeCapabilityMatrixRequiresEachOperationExplicitly(t *testing.T) {
	providerConfig := domain.ProviderConfig{ID: "fixture", Label: "Fixture", Kind: domain.ProviderOpenAICompatible, Enabled: true, AudioInput: true}
	capabilities := adapterCapabilities(providerConfig, &realtimeCapabilityFixture{})

	if !capabilities.Realtime.Supports(RealtimeAppendAudio) || !capabilities.Realtime.Supports(RealtimeRequestResponse) {
		t.Fatal("declared realtime operations are unavailable")
	}
	missing := capabilities.Realtime.Missing(RealtimeUpdateImage, RealtimeInterrupt, RealtimeVPNFailClosed)
	if len(missing) != 3 {
		t.Fatalf("missing operations=%v", missing)
	}
	if !capabilities.Media.Audio {
		t.Fatal("media capability was lost while describing realtime")
	}
}

func TestRegistryRejectsRealtimeBeforeAnyNetworkFallback(t *testing.T) {
	cfg := config.Default()
	registry, err := NewRegistry(cfg, testProxyResolver{err: fmt.Errorf("network should not be consulted")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(registry.Close)

	_, err = registry.OpenRealtime(context.Background(), RealtimeOpenRequest{ProviderID: cfg.Providers[1].ID, ModelID: "model", Credential: "secret"})
	if err == nil || OutcomeOf(err) != domain.OutcomeUnsupportedModality {
		t.Fatalf("unsupported realtime open=%v", err)
	}
}

func TestRegistryOpensOnlyExplicitRealtimeFactory(t *testing.T) {
	cfg := config.Default()
	registry, err := NewRegistry(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(registry.Close)
	registry.Set("fixture", &realtimeCapabilityFixture{})

	session, err := registry.OpenRealtime(context.Background(), RealtimeOpenRequest{ProviderID: "fixture", ModelID: "model", Credential: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	if !session.Capabilities().Supports(RealtimeAppendAudio) {
		t.Fatal("opened session lost declared capability")
	}
}
