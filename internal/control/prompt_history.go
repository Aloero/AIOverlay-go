package control

import (
	"aioverlay/internal/engine"
	"net/http"
)

type LastPrompts struct {
	RunID      string                  `json:"run_id"`
	PipelineID string                  `json:"pipeline_id"`
	Items      []engine.PromptSnapshot `json:"items"`
}

func (j *JobManager) LastPrompts() LastPrompts {
	j.mu.Lock()
	defer j.mu.Unlock()
	return LastPrompts{RunID: j.job.ID, PipelineID: j.job.Trace.PipelineID, Items: append([]engine.PromptSnapshot{}, j.prompts...)}
}
func (j *JobManager) rememberPrompt(id string, p engine.PromptSnapshot) {
	p = engine.BoundPromptSnapshot(p)
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.job.ID != id || j.job.Cleared {
		return
	}
	for i, old := range j.prompts {
		if old.NodeID == p.NodeID && old.ProviderID == p.ProviderID && old.ModelID == p.ModelID {
			j.prompts = append(j.prompts[:i], j.prompts[i+1:]...)
			break
		}
	}
	j.prompts = append(j.prompts, p)
	if len(j.prompts) > 16 {
		copy(j.prompts, j.prompts[len(j.prompts)-16:])
		j.prompts = j.prompts[:16]
	}
}
func (s *Server) handleLastPrompts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, s.jobs.LastPrompts())
}
