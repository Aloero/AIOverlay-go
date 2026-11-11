package control

import "net/http"

func (s *Server) handleCatalogSelection(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.WriteHeader(405)
		return
	}
	var q CatalogQuery
	if decodeJSON(r, &q, 256<<10) != nil || len(q.References) > 500 {
		http.Error(w, "invalid catalogue shortcuts", 400)
		return
	}
	q.ReferencesOnly = true
	if q.Limit < 1 || q.Limit > 500 {
		q.Limit = 40
	}
	page, err := s.backend.Catalog(q)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	writeJSON(w, page)
}

// handleReadiness serves the aggregated "почему не готово" report. It is read-only and assembles
// verdicts the layers already hold, so no request goes out and no state changes - the panel a user
// opens while something is broken must never itself be a thing that can break a run.
func (s *Server) handleReadiness(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, s.backend.Readiness())
}
