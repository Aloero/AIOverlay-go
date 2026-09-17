package app

import (
	"aioverlay/internal/domain"
	"reflect"
	"sort"
)

// Labels, key priorities/policy and request limits belong to core state, not
// adapter process identity. Secret changes still release the old credential pool.
func sameConnections(a, b []domain.ProviderConfig) bool {
	normalize := func(in []domain.ProviderConfig) []domain.ProviderConfig {
		out := append([]domain.ProviderConfig(nil), in...)
		for i := range out {
			p := &out[i]
			p.Label = ""
			p.CatalogProviderID = ""
			p.ModelsURL = ""
			p.RequestDefaults = domain.RequestDefaults{}
			p.Extra = nil
			keys := append([]domain.Credential(nil), p.CredentialPool.Keys...)
			for j := range keys {
				keys[j].Label = ""
				keys[j].Priority = 0
				keys[j].Extra = nil
			}
			sort.Slice(keys, func(i, j int) bool { return keys[i].ID < keys[j].ID })
			p.CredentialPool = domain.CredentialPool{Keys: keys}
		}
		sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
		return out
	}
	return reflect.DeepEqual(normalize(a), normalize(b))
}
