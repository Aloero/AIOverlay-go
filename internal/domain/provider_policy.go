package domain

import "strings"

// ProviderPolicy is the set of facts about a provider kind that must be readable without building
// anything: the pure candidate planner, the config validator, the trace renderer and the catalog
// all need them, and none of them may construct an adapter (the planner deliberately runs with no
// provider registry).
//
// Before this table the same facts were re-derived by `switch kind` in providers, catalog, engine,
// app and config - fourteen places, three of them duplicated verbatim - so adding a runtime meant
// finding every block, and a missed block silently downgraded a capability. Now a new kind is one
// row here plus one factory registration in internal/providers; a test keeps the two in step.
type ProviderPolicy struct {
	// TransportLabel names the path in a trace. "" renders as the plain "Direct".
	TransportLabel string

	// Hosted means a local runtime provides the endpoint, so the connection has no base URL to
	// point at: OpenCode's child process or the private STT model.
	Hosted bool
	// Gateway means the hosted runtime fronts upstream providers, so catalog references are
	// namespaced "provider/model" and a model can be ready with no key at all when the runtime
	// itself publishes it.
	Gateway bool
	// LocalRuntime means the connection is configured through the private runtime section and
	// driven through the audio preview rather than a text stage.
	LocalRuntime bool
	// Unlisted means the kind has no catalog routes to show: it serves no models.dev list.
	Unlisted bool
	// RealtimeProtocol means a persistent session transport: the endpoint is wss, the Realtime
	// section applies, audio_input is decided by the adapter rather than the operator, only a
	// realtime event source may use it, and neither a stateless pool route nor a one-shot probe
	// can meaningfully exercise it.
	RealtimeProtocol bool
	// ForbidsNetworkSettings means the runtime is entirely local, so a URL, a catalog provider,
	// headers or a credential environment in its config would be a lie about how it works.
	ForbidsNetworkSettings bool
	// ForbidsKeys means credentials belong to the runtime binary, not to this connection.
	ForbidsKeys bool
	// AnonymousCredentialID is the synthetic credential a connection with no configured keys runs
	// under ("" means it cannot run keyless at all). A gateway only qualifies while it fronts the
	// app's own pool, which is why this is a method on ProviderConfig rather than a plain field.
	AnonymousCredentialID string

	// SingleBody means the transport returns the whole answer at once, so a first-token deadline
	// has nothing to measure. An adapter may override it with Incremental().
	SingleBody bool
	// Media lists what the kind can carry. AudioFromConfig is the one per-connection refinement:
	// the OpenAI-compatible family carries audio optionally and only the operator knows whether
	// this endpoint does.
	Media        MediaPolicy
	AudioFromCfg bool

	// CatalogProviderID is the models.dev provider this kind's models belong to when the connection
	// does not state one. For a gateway it doubles as the id of its default upstream provider.
	CatalogProviderID string
	// ProbeBlockedReason, when set, means a single-shot connection probe cannot test this kind and
	// the UI must send the user to the transport that can.
	ProbeBlockedReason string
}

// MediaPolicy is the modality half of ProviderPolicy.
type MediaPolicy struct {
	Audio             bool
	Image             bool
	TranscriptionOnly bool
}

const (
	openCodeTransport = "Via OpenCode"
	localSTTTransport = "Local STT"
	liveProbeBlocked  = "OpenAI Live must be tested through a persistent Live source"
	localProbeBlocked = "local STT must be tested through the audio transcription preview"
)

// policies is the registry of kind facts. It must stay complete: an unregistered kind reports no
// capabilities at all, so a typo fails loudly at config load through KnownProviderKind rather than
// silently producing a provider that cannot carry anything.
var policies = map[ProviderKind]ProviderPolicy{
	ProviderOpenAICompatible: {
		Media:        MediaPolicy{Image: true},
		AudioFromCfg: true,
	},
	ProviderGemini: {
		Media:             MediaPolicy{Audio: true, Image: true},
		CatalogProviderID: "google",
	},
	ProviderTranscription: {
		SingleBody: true,
		Media:      MediaPolicy{Audio: true, TranscriptionOnly: true},
	},
	ProviderEmbeddedOpenCode: {
		TransportLabel:        openCodeTransport,
		Hosted:                true,
		Gateway:               true,
		AnonymousCredentialID: "keyless",
		SingleBody:            true,
		Media:                 MediaPolicy{Image: true},
		// Overridable per connection: the runtime can front any upstream provider.
		CatalogProviderID: "opencode",
	},
	ProviderOpenAILive: {
		RealtimeProtocol:   true,
		Media:              MediaPolicy{Audio: true},
		ProbeBlockedReason: liveProbeBlocked,
	},
	ProviderLocalSTT: {
		TransportLabel:         localSTTTransport,
		Hosted:                 true,
		LocalRuntime:           true,
		Unlisted:               true,
		ForbidsNetworkSettings: true,
		ForbidsKeys:            true,
		AnonymousCredentialID:  "local",
		SingleBody:             true,
		Media:                  MediaPolicy{Audio: true, TranscriptionOnly: true},
		ProbeBlockedReason:     localProbeBlocked,
	},
}

// Policy returns the facts for a kind. An unregistered kind reports the zero value, so a caller
// that requires a capability rejects it instead of guessing from the name.
func Policy(kind ProviderKind) ProviderPolicy {
	return policies[kind]
}

// KnownProviderKind reports whether the kind has a policy row. Config validation uses it so an
// unknown protocol is a load error rather than a provider that cannot carry any modality.
func KnownProviderKind(kind ProviderKind) bool {
	_, ok := policies[kind]
	return ok
}

// ProviderRegistrations lists the kinds the shipped policies cover; it keeps the policy table and
// the adapter factories from drifting apart.
func ProviderRegistrations() []ProviderKind {
	out := make([]ProviderKind, 0, len(policies))
	for kind := range policies {
		out = append(out, kind)
	}
	return out
}

// GatewayProviderID is the upstream provider a gateway connection serves inside its host. It is
// the same value the models.dev catalog names, so one accessor serves routing, the protocol
// adapter and the trace. OpenCodeProviderID is the field existing configs already use.
func (p ProviderConfig) GatewayProviderID() string {
	if id := strings.TrimSpace(p.OpenCodeProviderID); id != "" {
		return id
	}
	return Policy(p.Kind).CatalogProviderID
}

// CatalogProvider is the models.dev provider whose models this connection serves. A gateway is
// identified by the upstream it fronts; other kinds state their own id, inherit the kind default,
// or are identified by their connection id.
func (p ProviderConfig) CatalogProvider() string {
	policy := Policy(p.Kind)
	if policy.Gateway {
		return p.GatewayProviderID()
	}
	if id := strings.TrimSpace(p.CatalogProviderID); id != "" {
		return id
	}
	if policy.CatalogProviderID != "" {
		return policy.CatalogProviderID
	}
	return p.ID
}

// ModelRef renders a catalog model id the way this connection names it: a gateway fronts several
// upstream providers, so its references carry the provider; every other transport is already
// scoped to one.
func (p ProviderConfig) ModelRef(providerID, modelID string) string {
	if Policy(p.Kind).Gateway {
		return providerID + "/" + modelID
	}
	return modelID
}

// StripModelRef removes the gateway namespace from a reference that was recorded with one, so a
// trace entry can be looked up in the catalog again.
func (p ProviderConfig) StripModelRef(ref string) string {
	if prefix := p.CatalogProvider() + "/"; Policy(p.Kind).Gateway && strings.HasPrefix(ref, prefix) {
		return strings.TrimPrefix(ref, prefix)
	}
	return ref
}

// ObservedRef normalizes a model reference to the one key a watched outcome is stored under, so an
// attempt and the catalog that later reports it cannot disagree about what they are talking about -
// the same class of mismatch that produced the original "Model not found" bug, one layer up.
func (p ProviderConfig) ObservedRef(modelID string) string {
	return p.ModelRef(p.CatalogProvider(), p.StripModelRef(modelID))
}

// AnonymousCredential is the synthetic credential a connection with no configured keys runs under:
// a gateway fronting the app's own pool runs "keyless", a local runtime runs "local". ok=false
// means the connection cannot run without a real key - including a gateway repointed at someone
// else's upstream, which needs that upstream's credential.
func (p ProviderConfig) AnonymousCredential() (Credential, bool) {
	policy := Policy(p.Kind)
	if policy.AnonymousCredentialID == "" {
		return Credential{}, false
	}
	if policy.Gateway && p.GatewayProviderID() != policy.CatalogProviderID {
		return Credential{}, false
	}
	return Credential{ID: policy.AnonymousCredentialID, Enabled: true}, true
}
