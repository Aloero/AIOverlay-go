package control

import (
	"aioverlay/internal/domain"
	"aioverlay/internal/engine"
	"net/http"
)

func (s *Server) handlePromptPreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.WriteHeader(405)
		return
	}
	var req struct {
		Prompt domain.PromptConfig `json:"prompt"`
		Values engine.PromptValues `json:"values"`
	}
	if err := decodeJSON(r, &req, 256<<10); err != nil {
		http.Error(w, "invalid prompt preview", 400)
		return
	}
	system, user := engine.RenderRequestPrompt(req.Prompt, req.Values)
	writeJSON(w, map[string]string{"system": system, "user": user})
}
