package providers

import (
	"context"
	"testing"

	"aioverlay/internal/domain"
)

type shapeProvider struct{ incremental bool }

func (p shapeProvider) ID() string { return "shape" }
func (p shapeProvider) Stream(context.Context, Request, string) (<-chan Event, <-chan error) {
	return nil, nil
}
func (p shapeProvider) Incremental() bool { return p.incremental }

func TestStreamsTokensAsksTheAdapterFirst(t *testing.T) {
	// A transport that says it streams streams, whatever its kind table claims.
	if !StreamsTokens(domain.ProviderConfig{Kind: domain.ProviderEmbeddedOpenCode}, shapeProvider{incremental: true}) {
		t.Fatal("the adapter declaration must outrank the kind table")
	}
	if StreamsTokens(domain.ProviderConfig{Kind: domain.ProviderOpenAICompatible}, shapeProvider{incremental: false}) {
		t.Fatal("an adapter that returns one body must not be treated as streaming")
	}
	// With no adapter to ask, the kind table answers, so a simulation without transports still
	// predicts the deadline behaviour of the real one.
	if StreamsTokens(domain.ProviderConfig{Kind: domain.ProviderTranscription}, nil) {
		t.Fatal("transcription returns one body")
	}
	if !StreamsTokens(domain.ProviderConfig{Kind: domain.ProviderOpenAICompatible}, nil) {
		t.Fatal("an ordinary chat transport streams tokens")
	}
}
