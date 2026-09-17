package session

import (
	"aioverlay/internal/domain"
	"testing"
	"time"
)

func testContext() domain.ContextConfig {
	return domain.ContextConfig{ID: "conversation", Name: "Conversation", Enabled: true, MaxAgeS: 120, MaxEntries: 3, MaxChars: 100, IncludeTranscript: true, IncludeAnswers: true, KeepLatestScreen: true, ScreenMaxAgeS: 30}
}
func TestContextGenerationRejectsLateResult(t *testing.T) {
	s := NewContextStore()
	c := testContext()
	g := s.Generation("p", c.ID)
	if !s.Append(c, "p", g, ContextEntry{Kind: "transcript", Text: "before", At: time.Now()}) {
		t.Fatal("append")
	}
	s.Clear("p", c.ID)
	if s.Append(c, "p", g, ContextEntry{Kind: "transcript", Text: "late", At: time.Now()}) {
		t.Fatal("late generation restored cleared context")
	}
	snap := s.Snapshot(c, "p", time.Now())
	if snap.Text != "" {
		t.Fatalf("%q", snap.Text)
	}
}
func TestContextSnapshotCutoffAndPinnedScreen(t *testing.T) {
	s := NewContextStore()
	c := testContext()
	g := s.Generation("p", c.ID)
	now := time.Now()
	s.Append(c, "p", g, ContextEntry{Kind: "transcript", Text: "first", At: now.Add(-time.Second)})
	s.Append(c, "p", g, ContextEntry{Kind: "transcript", Text: "future", At: now.Add(time.Second)})
	s.SetScreen(c, "p", g, []byte("screen"), now.Add(-time.Second))
	snap := s.Snapshot(c, "p", now)
	if snap.Text != "[transcript] first" || string(snap.ImageJPEG) != "screen" {
		t.Fatalf("%q %q", snap.Text, snap.ImageJPEG)
	}
	s.SetScreen(c, "p", g, []byte("new"), now.Add(time.Second))
	if string(snap.ImageJPEG) != "screen" {
		t.Fatal("snapshot mutated")
	}
}
func TestContextBounds(t *testing.T) {
	s := NewContextStore()
	c := testContext()
	c.MaxEntries = 2
	g := s.Generation("p", c.ID)
	now := time.Now()
	for _, x := range []string{"one", "two", "three"} {
		s.Append(c, "p", g, ContextEntry{Kind: "transcript", Text: x, At: now})
	}
	snap := s.Snapshot(c, "p", now)
	if len(snap.Entries) != 2 || snap.Entries[0].Text != "two" {
		t.Fatalf("%+v", snap.Entries)
	}
}
