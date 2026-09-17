package trace

import (
	"aioverlay/internal/domain"
	"testing"
)

func TestAuditTraceSnapshotsCannotMutateHistory(t *testing.T) {
	s := New(10)
	s.Add(domain.ExecutionTrace{ID: "one", Events: []domain.TraceEvent{{Kind: "attempt_end", Message: "original"}}})
	v := s.List()
	v[0].Events[0].Message = "changed outside store"
	got, _ := s.Get("one")
	if got.Events[0].Message != "original" {
		t.Fatal("List exposes mutable stored event slices")
	}
}
