package domain

import (
	"fmt"
	"strings"
)

// Provenance names who asserted something about a model or a connection. It exists because "ready"
// used to be one boolean with no author: the app's catalog said a model was available while the
// runtime that had to serve it had never agreed to anything, and the user saw a timeout. A verdict
// without an author cannot be questioned, and an unquestionable verdict cannot be fixed.
type Provenance string

const (
	// ProvenanceUnknown is the zero value: nothing has been said about this yet.
	ProvenanceUnknown Provenance = ""
	// ProvenanceConfig is the operator's own statement in the connection (a key, an endpoint, an
	// enabled flag). It describes intent, never a live service.
	ProvenanceConfig Provenance = "config"
	// ProvenanceAdapter is what the compiled transport declares about itself - the only authority on
	// what it can encode, and blind to whatever sits behind the endpoint.
	ProvenanceAdapter Provenance = "adapter"
	// ProvenanceCatalog is the bundled models.dev snapshot: metadata about a model that exists
	// somewhere, not proof that this endpoint serves it today.
	ProvenanceCatalog Provenance = "catalog"
	// ProvenanceHost is a statement from the model host itself: the runtime publishes this model, or
	// reports the list it will accept right now.
	ProvenanceHost Provenance = "host"
	// ProvenanceObserved is a fact the application watched happen: a probe or a real attempt returned
	// this outcome for this model. It is the strongest evidence and the oldest: it decays.
	ProvenanceObserved Provenance = "observed"
)

// Blocker is one machine-readable reason a model or connection cannot be used right now. The code is
// stable and localizable by the UI; the text explains it to a human; the source says who learned it.
type Blocker struct {
	Code   string     `json:"code"`
	Text   string     `json:"text"`
	Source Provenance `json:"source"`
}

// Blocker codes. Keep them coarse enough to act on: the UI and the trace show the text, a script can
// switch on the code. Adding a code is cheap; renaming one is a breaking change.
const (
	BlockDisabled         = "disabled"          // the connection is turned off
	BlockNoKey            = "no_key"            // no enabled credential with a secret
	BlockNoEndpoint       = "no_endpoint"       // a network transport with no base URL
	BlockRuntimeMissing   = "runtime_missing"   // the bundled host binary is not in the archive
	BlockNotOffered       = "not_offered"       // the host does not publish this model
	BlockNoRoute          = "no_route"          // no enabled connection maps to this model reference
	BlockModelRefused     = "model_refused"     // the host rejected this model in a real attempt
	BlockRateLimited      = "rate_limited"      // upstream refused for quota or rate
	BlockModality         = "modality"          // the transport cannot carry the required input
	BlockProbeUnsupported = "probe_unsupported" // a one-shot probe cannot test this kind
)

// NewBlocker builds a blocker with an explicit source. Sources are never inferred from the code: two
// different layers can produce the same code with very different meanings for the user.
func NewBlocker(code string, source Provenance, text string, args ...any) Blocker {
	if len(args) > 0 {
		text = fmt.Sprintf(text, args...)
	}
	return Blocker{Code: code, Text: text, Source: source}
}

// Verdict is a readiness decision together with its evidence and the reasons against it. This is the
// shape every layer should have carried from the start: Ready alone could not distinguish "configured
// and working" from "configured and never tried" from "the host refused it ten minutes ago".
type Verdict struct {
	Ready    bool       `json:"ready"`
	Evidence Provenance `json:"evidence,omitempty"`
	Blockers []Blocker  `json:"blockers,omitempty"`
}

// Ok records a positive decision and which layer made it. Blockers from weaker sources are dropped:
// once the host itself or a real attempt has spoken, an older complaint from the config is noise.
func Ok(source Provenance) Verdict {
	return Verdict{Ready: true, Evidence: source}
}

// NotReady records a negative decision with at least one reason. The evidence is the strongest source
// among the reasons, so a caller cannot mislabel a verdict: if the host refused the model, the verdict
// says "host" even when the config is also incomplete. An empty reason list is a bug, not an unknown -
// callers that truly know nothing return the zero Verdict.
func NotReady(reasons ...Blocker) Verdict {
	if len(reasons) == 0 {
		panic("domain: a not-ready verdict needs a reason")
	}
	verdict := Verdict{Blockers: append([]Blocker(nil), reasons...)}
	for _, blocker := range reasons {
		if blocker.Source.Stronger(verdict.Evidence) {
			verdict.Evidence = blocker.Source
		}
	}
	return verdict
}

// Stronger reports whether this evidence outranks another. The order is the point: an observation is
// worth more than a publication, a publication more than metadata, metadata more than an intention.
func (p Provenance) Stronger(other Provenance) bool {
	return p.rank() > other.rank()
}

func (p Provenance) rank() int {
	switch p {
	case ProvenanceConfig:
		return 1
	case ProvenanceCatalog:
		return 2
	case ProvenanceAdapter:
		return 3
	case ProvenanceHost:
		return 4
	case ProvenanceObserved:
		return 5
	default:
		return 0
	}
}

// Status keeps the two labels the interface already renders. The reason lives in Blockers, so the
// label stays a summary instead of being the only available statement.
func (v Verdict) Status() string {
	if v.Ready {
		return "CONFIGURED"
	}
	return "NOT CONFIGURED"
}

// Reason returns the most informative blocker text: the strongest source first, because "the host
// refused this model" beats "no key configured" even when both are true.
func (v Verdict) Reason() string {
	best := -1
	var text string
	for _, b := range v.Blockers {
		if b.Source.rank() > best {
			best, text = b.Source.rank(), strings.TrimSpace(b.Text)
		}
	}
	return text
}
