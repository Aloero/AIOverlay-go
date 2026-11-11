package catalog

import (
	"strings"
	"time"

	"aioverlay/internal/domain"
)

// Observation is one outcome the application actually watched happen for one model reference. It is
// the only evidence in this package that comes from a live answer rather than from a file, which is
// exactly why it is kept separate, dated, and short-lived.
type Observation struct {
	ConnectionID string         `json:"connection_id"`
	Ref          string         `json:"ref"`
	Outcome      domain.Outcome `json:"outcome"`
	Text         string         `json:"text"`
	Code         string         `json:"code,omitempty"`
	Blocked      bool           `json:"blocked"`
	Recorded     time.Time      `json:"recorded"`
}

// observationTTL bounds how long a watched failure may hold a model back. The app cannot tell an
// upstream that retired a model from one that was briefly overloaded, so it stops blocking on the
// evidence instead of pretending to know; the observation stays reportable as history.
const observationTTL = 15 * time.Minute

// observations is bounded and in-memory. Nothing here survives a restart on purpose: a refusal seen
// by the previous process describes a previous moment, and reviving it would recreate the exact bug
// this layer exists to remove - an assertion the current host never agreed to.
type observations struct {
	byRef map[string]Observation
	order []string
}

func newObservations() *observations {
	return &observations{byRef: map[string]Observation{}}
}

// classify turns an attempt outcome into whether it should hold the model back. A timeout says
// nothing about the model - the request may have died anywhere - so it is recorded and not blocked.
func classify(outcome domain.Outcome, text string) (code string, blocks bool) {
	switch outcome {
	case domain.OutcomeModelUnavailable:
		// The runtime said it does not serve this model - the exact divergence this layer exists for.
		return domain.BlockModelRefused, true
	case domain.OutcomeRateLimited, domain.OutcomeQuotaExceeded:
		return domain.BlockRateLimited, true
	case domain.OutcomeProviderUnavailable:
		// Providers surface quota and retirement inside generic 5xx/unavailable bodies, so the text
		// is the only signal. Matching is deliberately narrow: an unknown 503 must not silently
		// retire a model.
		lowered := strings.ToLower(text)
		switch {
		case strings.Contains(lowered, "model not found"), strings.Contains(lowered, "no such model"),
			strings.Contains(lowered, "did you mean"):
			return domain.BlockModelRefused, true
		case strings.Contains(lowered, "free usage limit"), strings.Contains(lowered, "quota"),
			strings.Contains(lowered, "rate limit"), strings.Contains(lowered, "too many requests"):
			return domain.BlockRateLimited, true
		}
	}
	return "", false
}

func (o *observations) record(connectionID, ref string, outcome domain.Outcome, text string, at time.Time) {
	code, blocks := classify(outcome, text)
	key := observationKey(connectionID, ref)
	// Bounded, oldest-first eviction, and never the entry being rewritten: an unbounded map of
	// failures would outlive the incident it describes.
	if len(o.order) >= 256 {
		for i, other := range o.order {
			if other != key {
				delete(o.byRef, other)
				o.order = append(o.order[:i], o.order[i+1:]...)
				break
			}
		}
	}
	if _, exists := o.byRef[key]; !exists {
		o.order = append(o.order, key)
	}
	o.byRef[key] = Observation{ConnectionID: connectionID, Ref: ref, Outcome: outcome, Text: text, Code: code, Blocked: blocks, Recorded: at}
}

// get returns a watched outcome and whether it is still fresh.
func observationKey(connectionID, ref string) string { return connectionID + "\x00" + ref }

func (o *observations) get(connectionID, ref string, now time.Time) (Observation, bool) {
	watch, found := o.byRef[observationKey(connectionID, ref)]
	if !found {
		return Observation{}, false
	}
	// `fresh` says the observation is recent enough to mean anything. Whether it *holds a model
	// back* is the caller's reading of Blocked: the same lookup answers "should this block routing?"
	// and "what happened last?", which are different questions.
	return watch, now.Sub(watch.Recorded) <= observationTTL
}

// clear drops every watched outcome. A catalog refresh or a host change replaces the data the
// refusals referred to, so keeping them would block models the new list has just re-added.
func (o *observations) clear() {
	o.byRef = map[string]Observation{}
	o.order = nil
}
