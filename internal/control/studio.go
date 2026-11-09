package control

import (
	"aioverlay/internal/buildinfo"
	"aioverlay/internal/domain"
	"aioverlay/internal/engine"
	"context"
	"net/http"
	"time"
)

type studioBackend interface {
	StudioState() StudioState
	Simulate(context.Context, engine.SimulationRequest) (engine.SimulationReport, error)
	ProbeConnection(context.Context, engine.ConnectionProbe, engine.RunOptions) (engine.Result, error)
}

func (s *Server) handleStudioState(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		w.WriteHeader(405)
		return
	}
	b, ok := s.backend.(studioBackend)
	if !ok {
		writeJSON(w, StudioState{ActivePipelineID: s.backend.GetConfig().ActivePipelineID, Version: buildinfo.Version})
		return
	}
	writeJSON(w, b.StudioState())
}

func (s *Server) handlePipelineBehavior(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Pipeline domain.Pipeline `json:"pipeline"`
	}
	if err := decodeJSON(r, &req, 4<<20); err != nil || req.Pipeline.ID == "" {
		http.Error(w, "invalid pipeline behavior request", http.StatusBadRequest)
		return
	}
	writeJSON(w, engine.DescribePipelineBehavior(req.Pipeline))
}

func (s *Server) handleSimulation(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.WriteHeader(405)
		return
	}
	b, ok := s.backend.(studioBackend)
	if !ok {
		http.Error(w, "simulation unavailable", 501)
		return
	}
	var req engine.SimulationRequest
	if err := decodeJSON(r, &req, 8<<20); err != nil {
		http.Error(w, "invalid simulation request", 400)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	res, err := b.Simulate(ctx, req)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	writeJSON(w, res)
}
func (s *Server) handleConnectionProbe(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.WriteHeader(405)
		return
	}
	b, ok := s.backend.(studioBackend)
	if !ok {
		http.Error(w, "connection test unavailable", 501)
		return
	}
	var req engine.ConnectionProbe
	if decodeJSON(r, &req, 16<<10) != nil || !req.Confirm {
		http.Error(w, "confirm the single model request first", 400)
		return
	}
	id, err := s.jobs.startTaskPurpose(s.ctx, "connection-test", "connection_test", func(ctx context.Context, opt engine.RunOptions) (engine.Result, *BenchmarkReport, error) {
		res, err := b.ProbeConnection(ctx, req, opt)
		return res, nil, err
	})
	if err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	writeJSON(w, map[string]string{"id": id})
}
func (s *Server) handleLimitsPreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.WriteHeader(405)
		return
	}
	var req struct {
		Slot     domain.ModelSlot       `json:"slot"`
		Budget   domain.ExecutionBudget `json:"budget"`
		Defaults domain.RequestDefaults `json:"defaults"`
	}
	if decodeJSON(r, &req, 32<<10) != nil {
		http.Error(w, "invalid limits preview", 400)
		return
	}
	// This route is pure and usable for a partially edited graph.
	if req.Slot.TimeoutMS < 0 || req.Slot.FirstTokenTimeoutMS < 0 {
		http.Error(w, "negative timeout", 400)
		return
	}
	writeJSON(w, map[string]any{"limits": engine.ResolveLimits(req.Slot, req.Budget, req.Defaults), "sources": engine.LimitSources(req.Slot, req.Budget, req.Defaults)})
}
