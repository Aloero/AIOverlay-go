package catalog

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"aioverlay/internal/domain"
)

// hostOffers reports a model the host publishes for its own pool, i.e. one it serves without a key
// of ours. Unknown here means unknown: readiness then falls back to holding a key.
func (s *Service) hostOffers(ref string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.offered[ref]
}

// Route keeps the configured transport separate from the catalog provider ID. Its verdict says
// whether the model can be used *and who says so*: a route can be ready because the operator
// configured a key, because the host publishes the model for free, or because an attempt just proved
// it - and it can be blocked by any of those layers independently.
type Route struct {
	ConfigID  string
	Transport string
	ModelID   string
	Model     domain.ModelMeta
	Verdict   domain.Verdict
}

// Ready is the plain answer the scheduler and the UI asked for before verdicts existed.
func (r Route) Ready() bool { return r.Verdict.Ready }

// offeredBlocker explains the only case a gateway route has that no other kind has: the model needs
// no key because the host publishes it, and if the published list does not contain it, there is
// nothing to fall back on.
func (s *Service) offeredBlocker(ref string) domain.Blocker {
	return domain.NewBlocker(domain.BlockNotOffered, domain.ProvenanceHost,
		"the host does not publish %s as a model it serves without a key (snapshot %s)", ref, s.OfferedVersion())
}

// ProviderID is the models.dev provider whose models a connection serves. The rule is a fact
// about the kind plus the connection's own override, so it lives in the domain policy table and
// the app does not have to repeat a kind switch to name what it is looking at.
func ProviderID(p domain.ProviderConfig) string { return p.CatalogProvider() }

func (s *Service) Directory() []domain.ProviderMeta {
	by := map[string]*domain.ProviderMeta{}
	for _, m := range s.Models() {
		p := by[m.ProviderID]
		if p == nil {
			p = &domain.ProviderMeta{ID: m.ProviderID, Name: m.ProviderName, CredentialEnv: append([]string(nil), m.CredentialEnv...)}
			by[m.ProviderID] = p
		}
		p.Models++
	}
	out := make([]domain.ProviderMeta, 0, len(by))
	for _, p := range by {
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

func (s *Service) runtimePresent() bool {
	paths := []string{filepath.Join(filepath.Dir(s.path), "..", "opencode", "opencode.exe")}
	if p := strings.TrimSpace(os.Getenv("AIOVERLAY_OPENCODE_BIN")); p != "" {
		paths = append(paths, p)
	}
	for _, p := range paths {
		if usableRuntimeFile(p) {
			return true
		}
	}
	return false
}

func usableRuntimeFile(path string) bool {
	st, err := os.Stat(path)
	if err != nil || st.IsDir() || st.Size() == 0 {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	head := make([]byte, 200)
	n, _ := f.Read(head)
	return !strings.HasPrefix(string(head[:n]), "version https://git-lfs.github.com/spec/v1")
}

func (s *Service) Routes(cfg domain.Config) []Route {
	return s.routes(cfg, s.now())
}

// routes takes the clock explicitly so a test can decide whether a watched failure is still fresh.
func (s *Service) routes(cfg domain.Config, now time.Time) []Route {
	models := s.Models()
	byProvider := map[string][]domain.ModelMeta{}
	for _, m := range models {
		byProvider[m.ProviderID] = append(byProvider[m.ProviderID], m)
	}
	out := make([]Route, 0, len(models))
	runtimeOK := s.runtimePresent()
	for _, p := range cfg.Providers {
		policy := domain.Policy(p.Kind)
		// A local runtime publishes no models.dev list, and a persistent Live session is not a
		// route a stateless pool can take; neither has anything to show in the directory.
		if policy.Unlisted || policy.RealtimeProtocol {
			continue
		}
		shared, hasKey := s.connectionBlockers(p, runtimeOK)
		transport := policy.TransportLabel
		if transport == "" {
			transport = "Direct"
		}
		for _, m := range byProvider[ProviderID(p)] {
			id := p.ModelRef(m.ProviderID, m.ModelID)
			blockers := append([]domain.Blocker(nil), shared...)
			// The strongest layer that said "yes" wins: a key is an intention, a published offering is
			// the host's own statement about this model.
			evidence := domain.ProvenanceConfig
			keyless := policy.Gateway && m.ProviderID == policy.CatalogProviderID && m.Free && s.hostOffers(id)
			if keyless {
				evidence = domain.ProvenanceHost
			}
			if !hasKey && !keyless {
				if policy.Gateway {
					blockers = append(blockers, domain.NewBlocker(domain.BlockNoKey, domain.ProvenanceConfig,
						"no enabled key, and the host does not offer this model for free"), s.offeredBlocker(id))
				} else {
					blockers = append(blockers, domain.NewBlocker(domain.BlockNoKey, domain.ProvenanceConfig, "connection %s has no enabled key", p.ID))
				}
			}
			// A watched refusal outranks every file: it is the only evidence produced by the endpoint
			// this route would actually call. It also expires - see observationTTL.
			if watch, fresh := s.observation(p.ID, id, now); fresh && watch.Blocked {
				blockers = append(blockers, domain.NewBlocker(watch.Code, domain.ProvenanceObserved,
					"%s refused this model: %s (%s)", p.ID, watch.Text, watch.Outcome))
			}
			verdict := domain.Ok(evidence)
			if witness, fresh := s.observation(p.ID, id, now); fresh && witness.Outcome == domain.OutcomeSuccess {
				verdict = domain.Ok(domain.ProvenanceObserved)
			}
			if len(blockers) > 0 {
				verdict = domain.NotReady(blockers...)
			}
			out = append(out, Route{ConfigID: p.ID, Transport: transport, ModelID: id, Model: m, Verdict: verdict})
		}
	}
	return out
}

// Annotated decorates the catalog with the verdict the configured connections produce for each
// model. One model can be reachable through two connections with different reasons, so the answer is
// the strongest available: ready if any route is ready (naming the layer that said so), and otherwise
// every distinct reason against it - "no key configured" and "the runtime refused it ten minutes ago"
// are different problems for the user, and collapsing them into one boolean is how both got missed.
func (s *Service) Annotated(cfg domain.Config) []domain.ModelMeta {
	verdicts := map[string]domain.Verdict{}
	for _, r := range s.Routes(cfg) {
		key := r.Model.ProviderID + "/" + r.Model.ModelID
		current, seen := verdicts[key]
		if !seen {
			verdicts[key] = r.Verdict
			continue
		}
		verdicts[key] = combineVerdicts(current, r.Verdict)
	}
	models := s.Models()
	for i := range models {
		m := &models[i]
		verdict, seen := verdicts[m.ProviderID+"/"+m.ModelID]
		if !seen {
			// No connection maps to this provider at all: that is a fact about the configuration, and
			// the only honest statement is that nothing was ever asserted.
			verdict = domain.NotReady(domain.NewBlocker(domain.BlockNoEndpoint,
				domain.ProvenanceConfig, "no configured connection serves %s", m.ProviderID))
		}
		m.Ready, m.Status, m.Evidence, m.Blockers = verdict.Ready, verdict.Status(), verdict.Evidence, verdict.Blockers
	}
	return models
}

// mergeBlockers unions two reasons without repeating the same complaint from the same layer, keeping
// the text deterministic so a report is diffable.
func mergeBlockers(a, b domain.Verdict) domain.Verdict {
	seen := map[string]bool{}
	merged := make([]domain.Blocker, 0, len(a.Blockers)+len(b.Blockers))
	for _, list := range [][]domain.Blocker{a.Blockers, b.Blockers} {
		for _, blocker := range list {
			key := string(blocker.Source) + "\x00" + blocker.Code
			if seen[key] {
				continue
			}
			seen[key] = true
			merged = append(merged, blocker)
		}
	}
	// NotReady derives the evidence from the strongest merged blocker; recomputing it here is what
	// mergeBlockers used to get wrong in silence.
	return domain.NotReady(merged...)
}

// connectionBlockers collects what the whole connection decides, independent of any model: "the
// connection is off" is the same reason for all fifty of its models, and repeating it per model hides
// the reasons that differ between them. It also answers hasKey, because every caller that asks about
// credentials asks about this same pool.
func (s *Service) connectionBlockers(p domain.ProviderConfig, runtimeOK bool) (blockers []domain.Blocker, hasKey bool) {
	for _, k := range p.CredentialPool.Keys {
		if k.Enabled && strings.TrimSpace(k.Secret) != "" {
			hasKey = true
			break
		}
	}
	policy := domain.Policy(p.Kind)
	if !p.Enabled {
		blockers = append(blockers, domain.NewBlocker(domain.BlockDisabled, domain.ProvenanceConfig, "connection %s is turned off", p.ID))
	}
	if policy.Gateway {
		if !runtimeOK {
			blockers = append(blockers, domain.NewBlocker(domain.BlockRuntimeMissing, domain.ProvenanceHost,
				"the embedded runtime for %s is not installed next to the application", p.ID))
		}
	} else if !policy.Hosted && strings.TrimSpace(p.BaseURL) == "" {
		// A hosted kind (local runtimes) provides its own endpoint; only a network transport is
		// missing something when its URL is blank.
		blockers = append(blockers, domain.NewBlocker(domain.BlockNoEndpoint, domain.ProvenanceConfig, "connection %s has no endpoint URL", p.ID))
	}
	return blockers, hasKey
}

// ConnectionVerdicts answers "is this connection usable at all" per configured provider - including
// the kinds the model directory does not list (a local runtime, a persistent Live session), which is
// exactly where a per-model answer says nothing. It deliberately stops at connection level: which
// model fails for which further reason is Route/Annotated territory, and merging the two would hide
// one behind the other.
func (s *Service) ConnectionVerdicts(cfg domain.Config) map[string]domain.Verdict {
	runtimeOK := s.runtimePresent()
	out := make(map[string]domain.Verdict, len(cfg.Providers))
	for _, p := range cfg.Providers {
		blockers, hasKey := s.connectionBlockers(p, runtimeOK)
		// A gateway keyless through its published pool and a local runtime run under a synthetic
		// credential; everything else is a network transport that cannot call out anonymously.
		if _, anonymous := p.AnonymousCredential(); !anonymous && !hasKey {
			blockers = append(blockers, domain.NewBlocker(domain.BlockNoKey, domain.ProvenanceConfig,
				"connection %s has no enabled key", p.ID))
		}
		if len(blockers) > 0 {
			out[p.ID] = domain.NotReady(blockers...)
		} else {
			out[p.ID] = domain.Ok(domain.ProvenanceConfig)
		}
	}
	return out
}

// VerdictsFor answers the pool editor's question - "these exact refs, can they be used, and if not,
// why" - by merging every configured route that serves a ref, the way Annotated does for the whole
// directory. A ref no connection serves is reported as such rather than skipped: silence about a
// dead pool entry is how "model not found" survived until request time.
func (s *Service) VerdictsFor(cfg domain.Config, refs []string) map[string]domain.Verdict {
	byRef := map[string][]Route{}
	for _, route := range s.Routes(cfg) {
		byRef[route.ModelID] = append(byRef[route.ModelID], route)
	}
	out := make(map[string]domain.Verdict, len(refs))
	for _, ref := range refs {
		routes, found := byRef[ref]
		if !found {
			out[ref] = domain.NotReady(domain.NewBlocker(domain.BlockNoRoute, domain.ProvenanceConfig,
				"no enabled connection serves %s", ref))
			continue
		}
		verdict := routes[0].Verdict
		for _, route := range routes[1:] {
			verdict = combineVerdicts(verdict, route.Verdict)
		}
		out[ref] = verdict
	}
	return out
}

// combineVerdicts is the merge Annotated and VerdictsFor share: ready wins over blocked, stronger
// evidence wins over weaker, and two blocked answers keep every distinct reason.
func combineVerdicts(current, next domain.Verdict) domain.Verdict {
	switch {
	case next.Ready && current.Ready:
		if next.Evidence.Stronger(current.Evidence) {
			current.Evidence = next.Evidence
		}
		return current
	case next.Ready:
		return next
	case current.Ready: // keep the ready answer, but a stronger reason is still worth reporting
		if next.Evidence.Stronger(current.Evidence) {
			return current
		}
		return current
	default:
		return mergeBlockers(current, next)
	}
}

// OfferedVersion names the published-offerings snapshot behind a host-level claim, or "no snapshot"
// when the app has none - which is itself the answer to "why does the app think this is unavailable".
func (s *Service) OfferedVersion() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if strings.TrimSpace(s.offeredSnapshot) == "" {
		return "no snapshot"
	}
	return s.offeredSnapshot
}

// observation returns a watched outcome for one model on one connection and whether it still holds the
// model back. Stale evidence is reported by Observations() but never blocks routing.
func (s *Service) observation(connectionID, ref string, now time.Time) (Observation, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.observed.get(connectionID, ref, now)
}
