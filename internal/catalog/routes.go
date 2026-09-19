package catalog

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"aioverlay/internal/domain"
)

// Route keeps the configured transport separate from the catalog provider ID.
// Ready means configured, never proof of a successful remote request.
type Route struct {
	ConfigID  string
	Transport string
	ModelID   string
	Model     domain.ModelMeta
	Ready     bool
}

func ProviderID(p domain.ProviderConfig) string {
	if p.Kind == domain.ProviderEmbeddedOpenCode {
		if p.OpenCodeProviderID == "" {
			return "opencode"
		}
		return p.OpenCodeProviderID
	}
	if p.CatalogProviderID != "" {
		return p.CatalogProviderID
	}
	if p.Kind == domain.ProviderGemini {
		return "google"
	}
	return p.ID
}

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
	models := s.Models()
	byProvider := map[string][]domain.ModelMeta{}
	for _, m := range models {
		byProvider[m.ProviderID] = append(byProvider[m.ProviderID], m)
	}
	out := make([]Route, 0, len(models))
	runtimeOK := s.runtimePresent()
	for _, p := range cfg.Providers {
		hasKey := false
		for _, k := range p.CredentialPool.Keys {
			if k.Enabled && strings.TrimSpace(k.Secret) != "" {
				hasKey = true
				break
			}
		}
		for _, m := range byProvider[ProviderID(p)] {
			id, transport := m.ModelID, "Direct"
			ready := p.Enabled && hasKey && strings.TrimSpace(p.BaseURL) != ""
			if p.Kind == domain.ProviderEmbeddedOpenCode {
				id, transport = m.ProviderID+"/"+m.ModelID, "Via OpenCode"
				keyless := m.ProviderID == "opencode" && m.Free && s.opencodeReady[id]
				ready = p.Enabled && runtimeOK && (hasKey || keyless)
			}
			out = append(out, Route{ConfigID: p.ID, Transport: transport, ModelID: id, Model: m, Ready: ready})
		}
	}
	return out
}

func (s *Service) Annotated(cfg domain.Config) []domain.ModelMeta {
	ready := map[string]bool{}
	for _, r := range s.Routes(cfg) {
		if r.Ready {
			ready[r.Model.ProviderID+"/"+r.Model.ModelID] = true
		}
	}
	models := s.Models()
	for i := range models {
		m := &models[i]
		m.Ready = ready[m.ProviderID+"/"+m.ModelID]
		m.Status = "NOT CONFIGURED"
		if m.Ready {
			m.Status = "CONFIGURED"
		}
	}
	return models
}
