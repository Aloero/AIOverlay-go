package control

import (
	"aioverlay/internal/domain"
	"aioverlay/internal/engine"
	"net/http"
)

type planningBackend interface {
	ProviderDirectory() []domain.ProviderMeta
	PreviewPool(domain.Config, domain.ModelPoolConfig, bool) engine.PoolPlan
}

func (s *Server) handleProviderDirectory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(405)
		return
	}
	b, ok := s.backend.(planningBackend)
	if !ok {
		http.Error(w, "provider directory unavailable", 501)
		return
	}
	writeJSON(w, b.ProviderDirectory())
}
func (s *Server) handlePoolPlan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(405)
		return
	}
	b, ok := s.backend.(planningBackend)
	if !ok {
		http.Error(w, "pool planning unavailable", 501)
		return
	}
	var req struct {
		Config domain.Config          `json:"config"`
		Pool   domain.ModelPoolConfig `json:"pool"`
		Image  bool                   `json:"image"`
	}
	if err := decodeJSON(r, &req, 8<<20); err != nil {
		http.Error(w, "invalid pool plan request", 400)
		return
	}
	if req.Config.Version == 0 {
		req.Config = s.backend.GetConfig()
	}
	writeJSON(w, b.PreviewPool(req.Config, req.Pool, req.Image))
}

type settingsBackend interface {
	SetActivePipeline(string) error
	SaveProvider(domain.ProviderConfig) error
}

func (s *Server) handleActivePipeline(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(405)
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if err := decodeJSON(r, &req, 1<<20); err != nil {
		http.Error(w, "invalid pipeline selection", 400)
		return
	}
	if b, ok := s.backend.(settingsBackend); ok {
		if err := b.SetActivePipeline(req.ID); err != nil {
			http.Error(w, err.Error(), 409)
			return
		}
		writeJSON(w, map[string]string{"active_pipeline_id": req.ID})
		return
	}
	cfg := s.backend.GetConfig()
	for _, p := range cfg.Pipelines {
		if p.ID == req.ID && p.Enabled {
			cfg.ActivePipelineID = p.ID
			if err := s.backend.SaveConfig(cfg); err != nil {
				http.Error(w, err.Error(), 409)
				return
			}
			writeJSON(w, map[string]string{"active_pipeline_id": p.ID})
			return
		}
	}
	http.Error(w, "pipeline is missing or disabled", 400)
}

// Saving a connection must not require committing an unfinished graph draft.
func (s *Server) handleProviderConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(405)
		return
	}
	var p domain.ProviderConfig
	if err := decodeJSON(r, &p, 2<<20); err != nil {
		http.Error(w, "invalid provider configuration", 400)
		return
	}
	if b, ok := s.backend.(settingsBackend); ok {
		if err := b.SaveProvider(p); err != nil {
			http.Error(w, err.Error(), 409)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
		return
	}
	cfg := s.backend.GetConfig()
	found := false
	for i := range cfg.Providers {
		if cfg.Providers[i].ID == p.ID {
			cfg.Providers[i] = p
			found = true
			break
		}
	}
	if !found {
		cfg.Providers = append(cfg.Providers, p)
	}
	if err := s.backend.SaveConfig(cfg); err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}
