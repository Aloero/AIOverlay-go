// Package modelhost isolates the thing that provides models from the protocol used to speak to
// it. A host owns the process or service lifecycle, its private storage, how a credential is
// injected, and the list of models it actually serves. An adapter owns request encoding, stream
// parsing and the mapping of upstream failures onto domain.Outcome.
//
// The split exists because those two axes vary independently. The embedded OpenCode runtime can
// be upgraded (its HTTP protocol stays), replaced by another local runtime, or dropped entirely
// in favour of a remote endpoint, while the chat/vision/audio contract the engine sees must not
// change. When host and protocol were one type, the app and the runtime each kept their own model
// catalog and disagreed at request time ("Model not found ... Did you mean: <retired model>"),
// and the disagreement was invisible to the user. Endpoint and Host.Served are the seams where
// that truth is supposed to surface.
package modelhost

import (
	"context"
	"errors"
	"strings"
	"time"
)

// StartupTimeout bounds how long a cold host may take to become usable. It is deliberately not
// the model-attempt timeout: booting a ~180 MB runtime that also loads a model catalog is
// infrastructure, not model latency. Measured on a slow 4-vCPU Windows VM, the embedded OpenCode
// server reported healthy at 4.2 / 5.1 / 5.9 / 7.7 / 7.8 / 9.7 / 10.3 / 11.2 / 14.3 / 14.8 / 17.6 s,
// so charging the boot to `timeout_ms` (14 s by default) made the provider unstartable there and
// the user saw only a timeout. Pool.Acquire waits out the attempt's own context while the host
// keeps booting in the background, so the next request finds a warm instance.
const StartupTimeout = 25 * time.Second

// Endpoint is everything an adapter needs to reach one host instance. It carries no process or
// catalog knowledge, which is why an adapter can be pointed at a locally started runtime and at a
// remote service without changing its request logic.
type Endpoint struct {
	// BaseURL is the API root, e.g. "http://127.0.0.1:54321". Paths are appended by the adapter.
	BaseURL string
	// Username/Password are basic-auth credentials the host chose for its own control API.
	// Password is a secret: it is never logged, traced or serialized.
	Username string
	Password string
	// Proxy is the egress the host itself must use ("" means direct). A locally started runtime
	// gets it as HTTP(S)_PROXY; a remote adapter would dial through it.
	Proxy string
	// Headers are additional request headers required by this endpoint.
	Headers map[string]string
	// Host identifies the implementation ("opencode", "hermes", "static") and Version its build,
	// so a trace can name what actually answered. Both are non-secret.
	Host    string
	Version string
}

// Authorization returns the Basic scheme value, or "" when the endpoint has no password.
func (e Endpoint) Authorization() string {
	if e.Password == "" {
		return ""
	}
	return basicAuth(e.Username, e.Password)
}

// Need describes what a caller requires from a host instance. Everything that would change how
// the host has to be started belongs here, because only the host knows which of these values make
// two of its instances incompatible; Pool keeps that decision out of the scheduler.
type Need struct {
	// Credential is the provider key to inject. A host must not reuse an instance across keys.
	Credential string
	// Proxy is the egress the started host must use.
	Proxy string
	// Catalog pins the model snapshot the host has to agree with ("" means the host's default).
	// Without it, a host that refreshes its own list drifts away from the one the app validated.
	Catalog string
	// OutputTokenCap is a request-side limit some runtimes can only be told about at boot.
	OutputTokenCap int
}

// Instance is a live host instance owned by the pool.
type Instance interface {
	Alive() bool
	Stop()
}

// ModelRef is one model a host says it can serve right now.
type ModelRef struct {
	ProviderID string
	ModelID    string
}

// Ref renders the reference the way the catalog names it ("opencode/glm-5.3-flash-free").
func (m ModelRef) Ref() string {
	if strings.TrimSpace(m.ProviderID) == "" {
		return m.ModelID
	}
	return m.ProviderID + "/" + m.ModelID
}

// ErrStartupTimeout wraps a host that did not become usable inside its own startup budget. The
// adapter maps it onto an outcome; the host must not decide what a failed boot means for a request.
var ErrStartupTimeout = errors.New("model host did not become ready")

// ErrServedUnsupported is returned by a host that cannot enumerate what it serves. Callers must
// treat it as "unknown", never as "empty": an empty list would hide every model from the UI.
var ErrServedUnsupported = errors.New("model host does not publish its model list")

// Host provides models behind an Endpoint.
type Host interface {
	// ID names the implementation, e.g. "opencode".
	ID() string
	// Version is the host build ("1.18.31"), "" when the host cannot know it.
	Version() string
	// Endpoint returns a usable endpoint, a release func and an error. The host starts or warms an
	// instance as needed; the caller must call release (idempotent) when it stops using it. The
	// context bounds only this wait - the pool keeps booting when the attempt gives up, so a slow
	// machine still reaches a warm instance instead of failing every request.
	Endpoint(ctx context.Context, need Need) (Endpoint, func(), error)
	// Served reports the models the host actually accepts. A host that cannot say returns
	// ErrServedUnsupported.
	Served(ctx context.Context, need Need) ([]ModelRef, error)
	// Close stops every instance the host retains.
	Close()
}
