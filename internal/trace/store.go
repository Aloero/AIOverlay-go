package trace

import (
	"sync"

	"aioverlay/internal/domain"
)

type Store struct {
	mu    sync.RWMutex
	max   int
	items []domain.ExecutionTrace
}

func New(max int) *Store {
	if max <= 0 {
		max = 100
	}
	return &Store{max: max}
}
func (s *Store) Add(t domain.ExecutionTrace) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items = append([]domain.ExecutionTrace{domain.CloneTrace(t)}, s.items...)
	if len(s.items) > s.max {
		s.items = s.items[:s.max]
	}
}
func (s *Store) List() []domain.ExecutionTrace {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]domain.ExecutionTrace, len(s.items))
	for i, t := range s.items {
		out[i] = domain.CloneTrace(t)
	}
	return out
}
func (s *Store) Get(id string) (domain.ExecutionTrace, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, x := range s.items {
		if x.ID == id {
			return domain.CloneTrace(x), true
		}
	}
	return domain.ExecutionTrace{}, false
}
