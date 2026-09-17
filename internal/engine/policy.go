package engine

import (
	"math/rand/v2"
	"sort"
	"strings"
	"time"

	"aioverlay/internal/domain"
)

type modelStat struct {
	Success     int
	Failure     int
	AvgTTFT     float64
	AvgDuration float64
}

func (e *Engine) orderModelsForPolicy(nodeID string, policy domain.PoolPolicy, models []domain.ModelSlot) []domain.ModelSlot {
	out := append([]domain.ModelSlot(nil), models...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Priority > out[j].Priority })
	e.mu.Lock()
	defer e.mu.Unlock()
	switch policy {
	case domain.PoolRoundRobin:
		rotateSlots(out, e.nextCursorLocked("model:"+nodeID, len(out)))
	case domain.PoolFastest:
		sort.SliceStable(out, func(i, j int) bool {
			a, b := e.stats[modelKey(out[i])], e.stats[modelKey(out[j])]
			if a.Success > 0 && b.Success == 0 {
				return true
			}
			if b.Success > 0 && a.Success == 0 {
				return false
			}
			if a.Success > 0 && b.Success > 0 && a.AvgTTFT != b.AvgTTFT {
				return a.AvgTTFT < b.AvgTTFT
			}
			return out[i].Priority > out[j].Priority
		})
	case domain.PoolWeighted:
		if len(out) > 1 {
			total := 0.0
			for _, m := range out {
				w := m.Weight
				if w <= 0 {
					w = 1
				}
				total += w
			}
			pick := rand.Float64() * total
			idx := 0
			for i, m := range out {
				w := m.Weight
				if w <= 0 {
					w = 1
				}
				pick -= w
				if pick <= 0 {
					idx = i
					break
				}
			}
			if idx > 0 {
				first := out[idx]
				copy(out[1:idx+1], out[:idx])
				out[0] = first
			}
		}
	}
	return out
}

func (e *Engine) orderCredentials(providerID string, pool domain.CredentialPool) []domain.Credential {
	e.mu.Lock()
	defer e.mu.Unlock()
	var keys []domain.Credential
	now := time.Now()
	for _, k := range pool.Keys {
		if !k.Enabled || strings.TrimSpace(k.Secret) == "" {
			continue
		}
		if until := e.cooldown[providerID+"/"+k.ID]; until.After(now) {
			continue
		}
		keys = append(keys, k)
	}
	sort.SliceStable(keys, func(i, j int) bool { return keys[i].Priority > keys[j].Priority })
	switch pool.Policy {
	case domain.CredentialRoundRobin:
		rotateCredentials(keys, e.nextCursorLocked("key:"+providerID, len(keys)))
	case domain.CredentialSticky:
		good := e.lastGood[providerID]
		if good != "" {
			for i, k := range keys {
				if k.ID == good && i > 0 {
					first := keys[i]
					copy(keys[1:i+1], keys[:i])
					keys[0] = first
					break
				}
			}
		}
	case domain.CredentialRandom:
		rand.Shuffle(len(keys), func(i, j int) { keys[i], keys[j] = keys[j], keys[i] })
	}
	return keys
}

func (e *Engine) markCredential(providerID string, key domain.Credential, outcome domain.Outcome, cooldownSeconds int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	switch outcome {
	case domain.OutcomeSuccess:
		e.lastGood[providerID] = key.ID
		delete(e.cooldown, providerID+"/"+key.ID)
	case domain.OutcomeAuthFailed:
		e.cooldown[providerID+"/"+key.ID] = time.Now().Add(24 * time.Hour)
	case domain.OutcomeRateLimited, domain.OutcomeQuotaExceeded:
		if cooldownSeconds <= 0 {
			cooldownSeconds = 60
		}
		e.cooldown[providerID+"/"+key.ID] = time.Now().Add(time.Duration(cooldownSeconds) * time.Second)
	}
}

func (e *Engine) recordModel(slot domain.ModelSlot, outcome domain.Outcome, ttft, duration time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()
	k := modelKey(slot)
	s := e.stats[k]
	if outcome == domain.OutcomeSuccess {
		s.Success++
		s.AvgTTFT = rolling(s.AvgTTFT, float64(ttft.Milliseconds()), s.Success)
		s.AvgDuration = rolling(s.AvgDuration, float64(duration.Milliseconds()), s.Success)
	} else {
		s.Failure++
	}
	e.stats[k] = s
}
func rolling(old, next float64, n int) float64 {
	if n <= 1 {
		return next
	}
	return old + (next-old)/float64(n)
}
func modelKey(m domain.ModelSlot) string { return m.ProviderID + "/" + m.ModelID }
func (e *Engine) nextCursorLocked(key string, n int) int {
	if n <= 0 {
		return 0
	}
	v := e.rr[key] % n
	e.rr[key] = (v + 1) % n
	return v
}
func rotateSlots(v []domain.ModelSlot, n int) {
	if len(v) == 0 || n <= 0 {
		return
	}
	tmp := append([]domain.ModelSlot(nil), v...)
	copy(v, append(tmp[n:], tmp[:n]...))
}
func rotateCredentials(v []domain.Credential, n int) {
	if len(v) == 0 || n <= 0 {
		return
	}
	tmp := append([]domain.Credential(nil), v...)
	copy(v, append(tmp[n:], tmp[:n]...))
}
