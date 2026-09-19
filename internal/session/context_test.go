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

func TestContextOrdersLateTranscriptByCaptureTimeBeforePruning(t *testing.T) {
	s := NewContextStore()
	c := testContext()
	c.MaxEntries = 2
	g := s.Generation("p", c.ID)
	now := time.Now()
	// Simulate STT completion order B, A, C even though capture order is A, B, C.
	if !s.UpsertTranscript(c, "p", g, ContextEntry{ID: "b", Text: "second", At: now.Add(-2 * time.Second), Revision: 1, Final: true}) {
		t.Fatal("append b")
	}
	if !s.UpsertTranscript(c, "p", g, ContextEntry{ID: "a", Text: "first", At: now.Add(-3 * time.Second), Revision: 1, Final: true}) {
		t.Fatal("append a")
	}
	if !s.UpsertTranscript(c, "p", g, ContextEntry{ID: "c", Text: "third", At: now.Add(-time.Second), Revision: 1, Final: true}) {
		t.Fatal("append c")
	}
	snap := s.Snapshot(c, "p", now)
	if len(snap.Entries) != 2 || snap.Entries[0].ID != "b" || snap.Entries[1].ID != "c" {
		t.Fatalf("chronological retention failed: %+v", snap.Entries)
	}
	if snap.Text != "[transcript] second\n[transcript] third" {
		t.Fatalf("context=%q", snap.Text)
	}
}

func TestTranscriptPartialFinalLatePartialKeepsOneFinalRecord(t *testing.T) {
	s := NewContextStore()
	c := testContext()
	g := s.Generation("p", c.ID)
	now := time.Now()
	if !s.UpsertTranscript(c, "p", g, ContextEntry{ID: "seg", Text: "hel", At: now, Revision: 1}) {
		t.Fatal("initial partial rejected")
	}
	if !s.UpsertTranscript(c, "p", g, ContextEntry{ID: "seg", Text: "hello", At: now, Revision: 2, Final: true}) {
		t.Fatal("final rejected")
	}
	if s.UpsertTranscript(c, "p", g, ContextEntry{ID: "seg", Text: "hello?", At: now, Revision: 3}) {
		t.Fatal("late partial replaced final")
	}
	snap := s.Snapshot(c, "p", now.Add(time.Second))
	if len(snap.Entries) != 1 || snap.Entries[0].Text != "hello" || !snap.Entries[0].Final || snap.Entries[0].Revision != 2 {
		t.Fatalf("unexpected transcript state: %+v", snap.Entries)
	}
}

func TestOverlappingTranscriptTrimsOnlySharedAudioPrefix(t *testing.T) {
	store := NewContextStore()
	cfg := testContext()
	generation := store.Generation("p", cfg.ID)
	start := time.Now()
	first := ContextEntry{ID: "a", SourceID: "speaker", Text: "alpha beta gamma", Start: start, End: start.Add(time.Second), Revision: 1, Final: true}
	second := ContextEntry{ID: "b", SourceID: "speaker", Text: "Gamma, delta", Start: start.Add(800 * time.Millisecond), End: start.Add(1800 * time.Millisecond), Revision: 1, Final: true}
	third := ContextEntry{ID: "c", SourceID: "speaker", Text: "delta delta", Start: start.Add(1800 * time.Millisecond), End: start.Add(2 * time.Second), Revision: 1, Final: true}
	for _, entry := range []ContextEntry{first, second, third} {
		if !store.UpsertTranscript(cfg, "p", generation, entry) {
			t.Fatalf("rejected transcript %s", entry.ID)
		}
	}

	snapshot := store.Snapshot(cfg, "p", start.Add(3*time.Second))
	if len(snapshot.Entries) != 3 {
		t.Fatalf("entries=%+v", snapshot.Entries)
	}
	if snapshot.Entries[1].Text != "delta" {
		t.Fatalf("overlap was not trimmed: %q", snapshot.Entries[1].Text)
	}
	if snapshot.Entries[2].Text != "delta delta" {
		t.Fatalf("non-overlapping real repetition was removed: %q", snapshot.Entries[2].Text)
	}
}

func TestShortTranscriptCorpusPreservesNegationAndNumber(t *testing.T) {
	store := NewContextStore()
	cfg := testContext()
	generation := store.Generation("p", cfg.ID)
	start := time.Now()
	for i, text := range []string{"нет", "42"} {
		at := start.Add(time.Duration(i) * time.Second)
		if !store.UpsertTranscript(cfg, "p", generation, ContextEntry{ID: text, SourceID: "speaker", Text: text, Start: at, End: at.Add(100 * time.Millisecond), Revision: 1, Final: true}) {
			t.Fatalf("short transcript %q rejected", text)
		}
	}
	snapshot := store.Snapshot(cfg, "p", start.Add(3*time.Second))
	if len(snapshot.Entries) != 2 || snapshot.Entries[0].Text != "нет" || snapshot.Entries[1].Text != "42" {
		t.Fatalf("short transcripts changed: %+v", snapshot.Entries)
	}
}

func TestSummaryRequiresProvenanceAndIsOptInForFutureSnapshots(t *testing.T) {
	store := NewContextStore()
	cfg := testContext()
	generation := store.Generation("p", cfg.ID)
	now := time.Now()
	if store.UpsertSummary(cfg, "p", generation, ContextEntry{ID: "summary", Text: "summary", At: now, Final: true}) {
		t.Fatal("summary without provenance was stored")
	}
	entry := ContextEntry{ID: "summary", Text: "summary", At: now, DerivedFrom: []string{"a", "b"}, Revision: 1, Final: true}
	if !store.UpsertSummary(cfg, "p", generation, entry) {
		t.Fatal("summary with provenance rejected")
	}
	snapshot := store.Snapshot(cfg, "p", now.Add(time.Second))
	if len(snapshot.Entries) != 0 {
		t.Fatalf("summary was read without opt-in: %+v", snapshot.Entries)
	}
	cfg.IncludeSummaries = true
	snapshot = store.Snapshot(cfg, "p", now.Add(time.Second))
	if len(snapshot.Entries) != 1 || snapshot.Entries[0].Kind != "summary" || len(snapshot.Entries[0].DerivedFrom) != 2 {
		t.Fatalf("summary provenance lost: %+v", snapshot.Entries)
	}
}
