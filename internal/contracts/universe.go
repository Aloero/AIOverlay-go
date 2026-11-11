package contracts

// Universe is the single model/settings surface: which applications are usable right now,
// which providers can be reached through which of them, and which models are actually
// selectable — configured-provider models plus every model the host publishes for free.
// It deliberately reuses the route verdicts the scheduler uses, so the settings page and the
// engine never disagree about what is available.
type Universe struct {
	Apps      []UniverseApp      `json:"apps"`
	Providers []UniverseProvider `json:"providers"`
	Models    []UniverseModel    `json:"models"`
	Snapshot  string             `json:"snapshot"`
}

type UniverseApp struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Available bool   `json:"available"`
	Version   string `json:"version,omitempty"`
}

// UniverseProvider is one provider as the settings page shows it: what can serve it (Apps),
// whether the user registered it and through which app (RegisteredVia), and whether a key
// exists (Keyed). Free host-offered providers legitimately have no registration.
type UniverseProvider struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Models        int      `json:"models"`
	Apps          []string `json:"apps"`
	RegisteredVia string   `json:"registered_via,omitempty"`
	Keyed         bool     `json:"keyed"`
}

// UniverseModel is one selectable model. Ready follows the route verdict; Reason is the bounded,
// secret-free blocker text for the not-ready case. Apps lists every application that can carry
// the model right now.
type UniverseModel struct {
	Ref             string   `json:"ref"`
	ProviderID      string   `json:"provider_id"`
	ModelID         string   `json:"model_id"`
	InputModalities []string `json:"input_modalities"`
	Free            bool     `json:"free"`
	Ready           bool     `json:"ready"`
	Reason          string   `json:"reason,omitempty"`
	Apps            []string `json:"apps"`
}
