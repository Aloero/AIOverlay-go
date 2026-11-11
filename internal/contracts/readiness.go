package contracts

import (
	"time"

	"aioverlay/internal/catalog"
	"aioverlay/internal/domain"
	"aioverlay/internal/providers"
)

// ReadinessReport is the one aggregated answer to "why is the assistant not ready". Every layer kept
// its own boolean before this - the catalog said configured, the registry said vision, the runtime
// knew it was out of quota - and none of them was wrong, which is precisely why the user saw a bare
// timeout. The report does not decide anything new: it joins the verdicts the layers already produce
// and names the author of each.
type ReadinessReport struct {
	Catalog     CatalogFacts          `json:"catalog"`
	Connections []ConnectionReadiness `json:"connections"`
	Models      []ModelReadiness      `json:"models"`
	// Observations is the recent watched history, newest first - including ones that no longer
	// block. "It failed twice an hour ago and works now" is part of the honest answer.
	Observations []catalog.Observation `json:"observations,omitempty"`
}

// CatalogFacts names the data the readiness claims rest on. Without these, "the host does not
// publish this model" is timeless again - the very failure mode the verdict layer removed.
type CatalogFacts struct {
	Models          int       `json:"models"`
	LastRefresh     time.Time `json:"last_refresh"`
	LastError       string    `json:"last_error,omitempty"`
	OfferedSnapshot string    `json:"offered_snapshot"`
	RuntimeVersion  string    `json:"runtime_version,omitempty"`
}

// ConnectionReadiness pairs the transport's own capability declaration with the catalog's verdict on
// the connection. They are deliberately separate columns: "this transport can carry images" and "no
// usable model is behind it" are both true at once, and collapsing them is what made the overlay's
// error unreadable.
type ConnectionReadiness struct {
	Adapter providers.AdapterCapabilities `json:"adapter"`
	Verdict domain.Verdict                `json:"verdict"`
}

// ModelReadiness is one model reference used by a pipeline: the strongest available verdict, every
// distinct reason, and which pool asked for it, so the answer points at a place the user can fix.
type ModelReadiness struct {
	Ref     string         `json:"ref"`
	UsedBy  []string       `json:"used_by"`
	Verdict domain.Verdict `json:"verdict"`
	Reason  string         `json:"reason,omitempty"`
}
