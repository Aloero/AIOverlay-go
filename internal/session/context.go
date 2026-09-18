package session

import (
	"aioverlay/internal/domain"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

type ContextEntry struct {
	ID       string    `json:"id"`
	Kind     string    `json:"kind"`
	SourceID string    `json:"source_id,omitempty"`
	Text     string    `json:"text,omitempty"`
	At       time.Time `json:"at"`
	Start    time.Time `json:"start,omitempty"`
	End      time.Time `json:"end,omitempty"`
	Gap      bool      `json:"gap,omitempty"`
	Revision uint64    `json:"revision,omitempty"`
	Final    bool      `json:"final,omitempty"`
}

type ContextSnapshot struct {
	ID         string         `json:"id"`
	Generation uint64         `json:"generation"`
	Cutoff     time.Time      `json:"cutoff"`
	Text       string         `json:"text"`
	ImageJPEG  []byte         `json:"-"`
	ImageAt    time.Time      `json:"image_at,omitempty"`
	Entries    []ContextEntry `json:"entries"`
	Truncated  bool           `json:"truncated"`
}

type contextState struct {
	generation uint64
	entries    []ContextEntry
	image      []byte
	imageAt    time.Time
}

type ContextStore struct {
	mu     sync.RWMutex
	states map[string]*contextState
}

func NewContextStore() *ContextStore                 { return &ContextStore{states: map[string]*contextState{}} }
func contextKey(pipelineID, contextID string) string { return pipelineID + "\x00" + contextID }
func (s *ContextStore) stateLocked(pipelineID, contextID string) *contextState {
	k := contextKey(pipelineID, contextID)
	st := s.states[k]
	if st == nil {
		st = &contextState{generation: 1}
		s.states[k] = st
	}
	return st
}
func (s *ContextStore) Generation(pipelineID, contextID string) uint64 {
	if contextID == "" {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stateLocked(pipelineID, contextID).generation
}
func (s *ContextStore) Clear(pipelineID, contextID string) uint64 {
	if contextID == "" {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.stateLocked(pipelineID, contextID)
	st.generation++
	st.entries = nil
	st.image = nil
	st.imageAt = time.Time{}
	return st.generation
}
func entryTime(e ContextEntry) time.Time {
	if !e.Start.IsZero() {
		return e.Start
	}
	return e.At
}

func sortEntries(entries []ContextEntry) {
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entryTime(entries[i]), entryTime(entries[j])
		return a.Before(b)
	})
}

func (s *ContextStore) Append(cfg domain.ContextConfig, pipelineID string, generation uint64, entry ContextEntry) bool {
	if !cfg.Enabled || cfg.ID == "" || (entry.Text == "" && !entry.Gap) {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.stateLocked(pipelineID, cfg.ID)
	if generation != 0 && generation != st.generation {
		return false
	}
	if entry.At.IsZero() {
		entry.At = time.Now()
	}
	st.entries = append(st.entries, entry)
	sortEntries(st.entries)
	s.pruneLocked(st, cfg, entry.At)
	return true
}

// UpsertTranscript keeps one chronological record per segment. Partial updates may
// advance a non-final record, while a final transcript is immutable against late
// partial callbacks. Final transcripts from deliberately overlapped audio segments
// trim only a matching suffix/prefix inside the overlapping capture interval.
func (s *ContextStore) UpsertTranscript(cfg domain.ContextConfig, pipelineID string, generation uint64, entry ContextEntry) bool {
	if !cfg.Enabled || cfg.ID == "" || entry.ID == "" || entry.Text == "" {
		return false
	}
	entry.Kind = "transcript"
	if entry.Revision == 0 {
		entry.Revision = 1
	}
	if entry.At.IsZero() {
		entry.At = entryTime(entry)
		if entry.At.IsZero() {
			entry.At = time.Now()
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.stateLocked(pipelineID, cfg.ID)
	if generation != 0 && generation != st.generation {
		return false
	}

	existing := transcriptIndex(st.entries, entry.ID)
	if existing >= 0 {
		old := st.entries[existing]
		if old.Final || entry.Revision <= old.Revision {
			return false
		}
	}
	if entry.Final {
		entry.Text = trimTranscriptOverlap(st.entries, entry)
		if entry.Text == "" {
			if existing >= 0 {
				st.entries = append(st.entries[:existing], st.entries[existing+1:]...)
			}
			return true
		}
	}
	if existing >= 0 {
		st.entries[existing] = entry
	} else {
		st.entries = append(st.entries, entry)
	}
	sortEntries(st.entries)
	s.pruneLocked(st, cfg, entry.At)
	return true
}

func transcriptIndex(entries []ContextEntry, id string) int {
	for i := range entries {
		if entries[i].Kind == "transcript" && entries[i].ID == id {
			return i
		}
	}
	return -1
}

const maxTranscriptOverlapTokens = 16

func trimTranscriptOverlap(entries []ContextEntry, current ContextEntry) string {
	if current.Start.IsZero() || current.End.IsZero() {
		return current.Text
	}
	previous := previousOverlappingTranscript(entries, current)
	if previous == nil {
		return current.Text
	}
	left, right := strings.Fields(previous.Text), strings.Fields(current.Text)
	limit := min(len(left), len(right), maxTranscriptOverlapTokens)
	for count := limit; count > 0; count-- {
		if equalTranscriptTokens(left[len(left)-count:], right[:count]) {
			return strings.Join(right[count:], " ")
		}
	}
	return current.Text
}

func previousOverlappingTranscript(entries []ContextEntry, current ContextEntry) *ContextEntry {
	var best *ContextEntry
	for i := range entries {
		candidate := &entries[i]
		if candidate.Kind != "transcript" || candidate.ID == current.ID || !candidate.Final || candidate.SourceID != current.SourceID {
			continue
		}
		if candidate.Start.IsZero() || candidate.End.IsZero() || !candidate.Start.Before(current.Start) || !current.Start.Before(candidate.End) {
			continue
		}
		if best == nil || best.End.Before(candidate.End) {
			best = candidate
		}
	}
	return best
}

func equalTranscriptTokens(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if normalizeTranscriptToken(left[i]) != normalizeTranscriptToken(right[i]) {
			return false
		}
	}
	return true
}

func normalizeTranscriptToken(token string) string {
	return strings.ToLower(strings.TrimFunc(token, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	}))
}

func (s *ContextStore) SetScreen(cfg domain.ContextConfig, pipelineID string, generation uint64, image []byte, at time.Time) bool {
	if !cfg.Enabled || !cfg.KeepLatestScreen || cfg.ID == "" || len(image) == 0 {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.stateLocked(pipelineID, cfg.ID)
	if generation != 0 && generation != st.generation {
		return false
	}
	if at.IsZero() {
		at = time.Now()
	}
	st.image = append(st.image[:0], image...)
	st.imageAt = at
	return true
}
func (s *ContextStore) pruneLocked(st *contextState, cfg domain.ContextConfig, now time.Time) {
	s.pruneExpiredLocked(st, cfg, now)
	s.pruneEntryCountLocked(st, cfg.MaxEntries)
	s.pruneTextSizeLocked(st, cfg.MaxChars)
}

func (s *ContextStore) pruneExpiredLocked(st *contextState, cfg domain.ContextConfig, now time.Time) {
	if cfg.MaxAgeS <= 0 {
		return
	}
	cutoff := now.Add(-time.Duration(cfg.MaxAgeS) * time.Second)
	firstKept := 0
	for firstKept < len(st.entries) && entryTime(st.entries[firstKept]).Before(cutoff) {
		firstKept++
	}
	if firstKept > 0 {
		st.entries = append([]ContextEntry(nil), st.entries[firstKept:]...)
	}
}

func (s *ContextStore) pruneEntryCountLocked(st *contextState, maxEntries int) {
	if maxEntries <= 0 || len(st.entries) <= maxEntries {
		return
	}
	st.entries = append([]ContextEntry(nil), st.entries[len(st.entries)-maxEntries:]...)
}

func (s *ContextStore) pruneTextSizeLocked(st *contextState, maxChars int) {
	if maxChars <= 0 {
		return
	}
	totalChars := 0
	for _, entry := range st.entries {
		totalChars += len(entry.Text)
	}
	for totalChars > maxChars && len(st.entries) > 0 {
		totalChars -= len(st.entries[0].Text)
		st.entries = append([]ContextEntry(nil), st.entries[1:]...)
	}
}
func (s *ContextStore) Snapshot(cfg domain.ContextConfig, pipelineID string, cutoff time.Time) ContextSnapshot {
	out := ContextSnapshot{ID: cfg.ID, Cutoff: cutoff}
	if !cfg.Enabled || cfg.ID == "" {
		return out
	}
	if cutoff.IsZero() {
		cutoff = time.Now()
		out.Cutoff = cutoff
	}
	s.mu.Lock()
	st := s.stateLocked(pipelineID, cfg.ID)
	s.pruneLocked(st, cfg, cutoff)
	out.Generation = st.generation
	for _, e := range st.entries {
		if entryTime(e).After(cutoff) {
			continue
		}
		switch e.Kind {
		case "transcript":
			if !cfg.IncludeTranscript {
				continue
			}
		case "answer":
			if !cfg.IncludeAnswers {
				continue
			}
		}
		out.Entries = append(out.Entries, e)
	}
	if cfg.KeepLatestScreen && len(st.image) > 0 && !st.imageAt.After(cutoff) && (cfg.ScreenMaxAgeS <= 0 || cutoff.Sub(st.imageAt) <= time.Duration(cfg.ScreenMaxAgeS)*time.Second) {
		out.ImageJPEG = append([]byte(nil), st.image...)
		out.ImageAt = st.imageAt
	}
	s.mu.Unlock()
	sortEntries(out.Entries)
	var b strings.Builder
	for _, e := range out.Entries {
		if e.Gap {
			b.WriteString("[context gap]\n")
			continue
		}
		if e.Text == "" {
			continue
		}
		label := e.Kind
		if e.SourceID != "" {
			label += "/" + e.SourceID
		}
		fmt.Fprintf(&b, "[%s] %s\n", label, e.Text)
	}
	out.Text = strings.TrimSpace(b.String())
	return out
}
func (s *ContextStore) Status(pipelineID string, configs []domain.ContextConfig) []map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]map[string]any, 0, len(configs))
	for _, cfg := range configs {
		st := s.states[contextKey(pipelineID, cfg.ID)]
		row := map[string]any{"id": cfg.ID, "name": cfg.Name, "enabled": cfg.Enabled, "generation": uint64(1), "entries": 0, "has_screen": false}
		if st != nil {
			row["generation"] = st.generation
			row["entries"] = len(st.entries)
			row["has_screen"] = len(st.image) > 0
		}
		out = append(out, row)
	}
	return out
}
