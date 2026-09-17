package control

import (
	"aioverlay/internal/tunnel"
	"context"
	"net/http"
)

func (s *Server) handleVPNImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.WriteHeader(405)
		return
	}
	var req struct {
		Text string `json:"text"`
	}
	if decodeJSON(r, &req, 300<<10) != nil {
		http.Error(w, "invalid VPN import", 400)
		return
	}
	result, err := tunnel.ImportProfile(req.Text)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	writeJSON(w, result)
}
func (s *Server) handleVPNProbe(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.WriteHeader(405)
		return
	}
	var req struct {
		Target  string `json:"target"`
		Confirm bool   `json:"confirm"`
	}
	if decodeJSON(r, &req, 4<<10) != nil || !req.Confirm {
		http.Error(w, "confirm network connection check first", 400)
		return
	}
	if s.jobs.Snapshot().Running {
		http.Error(w, "stop the current AI request before a connection test", 409)
		return
	}
	b, ok := s.backend.(interface {
		ProbeVPN(context.Context, string) (tunnel.ProbeResult, error)
	})
	if !ok {
		http.Error(w, "VPN probe unavailable", 501)
		return
	}
	result, err := b.ProbeVPN(r.Context(), req.Target)
	if err != nil {
		http.Error(w, err.Error(), 502)
		return
	}
	writeJSON(w, result)
}
