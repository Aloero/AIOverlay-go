package control

import (
	"context"
	"net/http"
	"time"

	"aioverlay/internal/domain"
)

type vpnBackend interface {
	ValidateVPN(context.Context, domain.VPNConfig) error
}

func (s *Server) handleVPNCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	b, ok := s.backend.(vpnBackend)
	if !ok {
		http.Error(w, "AI VPN validation unavailable", http.StatusNotImplemented)
		return
	}
	var req domain.VPNConfig
	if err := decodeJSON(r, &req, 4<<20); err != nil {
		http.Error(w, "invalid AI VPN configuration", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	if err := b.ValidateVPN(ctx, req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "engine": "sing-box", "scope": "ai_requests_only"})
}
