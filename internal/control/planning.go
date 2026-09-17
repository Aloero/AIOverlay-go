package control

import (
	"aioverlay/internal/domain"
	"aioverlay/internal/engine"
	"aioverlay/internal/providers"
	"fmt"
	"net/http"
)

type planningBackend interface {
	ProviderDirectory() []domain.ProviderMeta
	PreviewPool(domain.Config, domain.ModelPoolConfig, bool) engine.PoolPlan
}

type capabilityBackend interface {
	ProviderCapabilities() []providers.AdapterCapabilities
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

func (s *Server) handleProviderCapabilities(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	b, ok := s.backend.(capabilityBackend)
	if !ok {
		http.Error(w, "provider capabilities unavailable", http.StatusNotImplemented)
		return
	}
	writeJSON(w, b.ProviderCapabilities())
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
		Config     domain.Config          `json:"config"`
		Pool       domain.ModelPoolConfig `json:"pool"`
		Image      bool                   `json:"image"`
		PipelineID string                 `json:"pipeline_id"`
		NodeID     string                 `json:"node_id"`
	}
	if err := decodeJSON(r, &req, 8<<20); err != nil {
		http.Error(w, "invalid pool plan request", 400)
		return
	}
	if req.Config.Version == 0 {
		req.Config = s.backend.GetConfig()
	}
	if advanced, ok := s.backend.(interface {
		PreviewOrderedPool(domain.Config, domain.ModelPoolConfig, bool, string, string) engine.PoolPlan
	}); ok {
		writeJSON(w, advanced.PreviewOrderedPool(req.Config, req.Pool, req.Image, req.PipelineID, req.NodeID))
		return
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
	err := s.jobs.WhenIdle(func() error {
		if b, ok := s.backend.(settingsBackend); ok {
			return b.SetActivePipeline(req.ID)
		}
		cfg := s.backend.GetConfig()
		for _, p := range cfg.Pipelines {
			if p.ID == req.ID && p.Enabled {
				cfg.ActivePipelineID = p.ID
				return s.backend.SaveConfig(cfg)
			}
		}
		return fmt.Errorf("pipeline is missing or disabled")
	})
	if err != nil {
		settingsError(w, err, 409)
		return
	}
	writeJSON(w, map[string]string{"active_pipeline_id": req.ID})
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
	err := s.jobs.WhenIdle(func() error {
		if b, ok := s.backend.(settingsBackend); ok {
			return b.SaveProvider(p)
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
		return s.backend.SaveConfig(cfg)
	})
	if err != nil {
		settingsError(w, err, 409)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}
