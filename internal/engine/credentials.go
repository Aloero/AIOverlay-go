package engine

import "aioverlay/internal/domain"

func (r *runner) allowedCredentials(provider string, keys []domain.Credential) []domain.Credential {
	r.providersMu.Lock()
	defer r.providersMu.Unlock()
	used := r.keysUsed[provider]
	cap := r.pipeline.Budget.MaxKeysPerProvider
	if cap <= 0 || len(used) < cap {
		return keys
	}
	out := make([]domain.Credential, 0, len(keys))
	for _, key := range keys {
		if used[key.ID] {
			out = append(out, key)
		}
	}
	return out
}
