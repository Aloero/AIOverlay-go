package session

import (
	"aioverlay/internal/domain"
	"aioverlay/internal/engine"
	paudio "aioverlay/internal/platform/audio"
	"context"
	"fmt"
	"testing"
	"time"
)

func auditEntries() (*ContextStore, domain.ContextConfig, uint64, time.Time) {
	s := NewContextStore()
	c := testContext()
	c.MaxChars = 10000
	c.MaxEntries = 20
	return s, c, s.Generation("p", c.ID), time.Unix(1700000000, 0)
}
func TestR2AuditOverlapPreservesNumericSign(t *testing.T) {
	s, c, g, at := auditEntries()
	a := ContextEntry{ID: "a", SourceID: "audio", Text: "temperature -5", Start: at, End: at.Add(time.Second), Revision: 1, Final: true}
	b := ContextEntry{ID: "b", SourceID: "audio", Text: "+5 degrees", Start: at.Add(800 * time.Millisecond), End: at.Add(1800 * time.Millisecond), Revision: 1, Final: true}
	s.UpsertTranscript(c, "p", g, a)
	s.UpsertTranscript(c, "p", g, b)
	got := s.Snapshot(c, "p", at.Add(2*time.Second))
	if len(got.Entries) != 2 || got.Entries[1].Text != "+5 degrees" {
		t.Fatalf("numeric sign was lost: %s", got.Text)
	}
}
func TestR2AuditOverlapPreservesCodeIdentifiers(t *testing.T) {
	s, c, g, at := auditEntries()
	for i, text := range []string{"use C++", "C# instead"} {
		start := at.Add(time.Duration(i) * 800 * time.Millisecond)
		s.UpsertTranscript(c, "p", g, ContextEntry{ID: []string{"a", "b"}[i], SourceID: "audio", Text: text, Start: start, End: start.Add(time.Second), Revision: 1, Final: true})
	}
	got := s.Snapshot(c, "p", at.Add(2*time.Second))
	if len(got.Entries) != 2 || got.Entries[1].Text != "C# instead" {
		t.Fatalf("code identifier was lost: %s", got.Text)
	}
}
func TestR2AuditLatePartialCannotReviveFullyDeduplicatedFinal(t *testing.T) {
	s, c, g, at := auditEntries()
	s.UpsertTranscript(c, "p", g, ContextEntry{ID: "a", SourceID: "audio", Text: "alpha", Start: at, End: at.Add(time.Second), Revision: 1, Final: true})
	b := ContextEntry{ID: "b", SourceID: "audio", Text: "alph", Start: at.Add(800 * time.Millisecond), End: at.Add(1800 * time.Millisecond), Revision: 1}
	s.UpsertTranscript(c, "p", g, b)
	b.Text = "alpha"
	b.Final = true
	b.Revision = 2
	s.UpsertTranscript(c, "p", g, b)
	b.Text = "late partial restored"
	b.Final = false
	b.Revision = 3
	if s.UpsertTranscript(c, "p", g, b) {
		t.Fatal("late partial accepted after final was deduplicated away")
	}
}
func TestR2AuditOverlapIndependentOfCompletionOrder(t *testing.T) {
	s, c, g, at := auditEntries()
	a := ContextEntry{ID: "a", SourceID: "audio", Text: "alpha beta", Start: at, End: at.Add(time.Second), Revision: 1, Final: true}
	b := ContextEntry{ID: "b", SourceID: "audio", Text: "beta gamma", Start: at.Add(800 * time.Millisecond), End: at.Add(1800 * time.Millisecond), Revision: 1, Final: true}
	s.UpsertTranscript(c, "p", g, b)
	s.UpsertTranscript(c, "p", g, a)
	got := s.Snapshot(c, "p", at.Add(2*time.Second))
	if len(got.Entries) != 2 || got.Entries[1].Text != "gamma" {
		t.Fatalf("completion order changed accepted transcript: %s", got.Text)
	}
}
func TestR2AuditLatestScreenRejectsOlderArrival(t *testing.T) {
	s, c, g, at := auditEntries()
	s.SetScreen(c, "p", g, []byte("new"), at.Add(time.Second))
	s.SetScreen(c, "p", g, []byte("old"), at)
	got := s.Snapshot(c, "p", at.Add(2*time.Second))
	if string(got.ImageJPEG) != "new" {
		t.Fatalf("late older screen replaced latest: %q", got.ImageJPEG)
	}
}
func TestR2AuditClearRemovesTranscriptFromStatus(t *testing.T) {
	m := NewManager(nil)
	m.cfg = managerConfig()
	r := &sourceRuntime{cfg: m.cfg.Pipelines[0].Sources[0]}
	m.sources[r.cfg.ID] = r
	c := m.cfg.Pipelines[0].Contexts[0]
	g := m.contexts.Generation("p", c.ID)
	turn := backgroundTurn{pipelineID: "p", ctxCfg: c, generation: g, input: engine.Input{EventID: "before-clear"}, at: time.Now()}
	m.transcriptObserver("p", r, turn)(engine.Observation{Kind: "audio.text", Text: "OLD_PRIVATE_CONVERSATION", SegmentID: "before-clear", Revision: 1, Final: true})
	if _, err := m.ClearContext(c.ID); err != nil {
		t.Fatal(err)
	}
	rows := m.Status()["sources"].([]SourceStatus)
	if len(rows) > 0 && rows[0].LastTranscript != "" {
		t.Fatalf("cleared conversation still exposed in status: %q", rows[0].LastTranscript)
	}
}
func TestR2AuditPendingWaitSeesSpeechStillInSegmenter(t *testing.T) {
	m := NewManager(nil)
	cfg := managerConfig()
	cfg.Pipelines[0].Commands[0].ContextID = "c"
	cfg.Pipelines[0].Commands[0].WaitForTranscriptMS = 20
	started := make(chan struct{})
	m.SetCapture(func(context.Context) ([]byte, error) { return []byte("screen"), nil }, nil)
	m.SetAudioCapture(func(ctx context.Context, _ domain.AudioSourceConfig, emit func(paudio.Block)) error {
		voice := make([]int16, 1600)
		for i := range voice {
			voice[i] = 12000
		}
		emit(paudio.Block{Rate: 16000, Samples: voice, At: time.Now().Add(-100 * time.Millisecond)})
		close(started)
		<-ctx.Done()
		return ctx.Err()
	})
	m.Start(context.Background(), cfg)
	defer m.Close()
	<-started
	p, err := m.PrepareCommand(context.Background(), "analyze")
	if err != nil {
		t.Fatal(err)
	}
	if !p.Input.ContextIncomplete {
		t.Fatal("ongoing pre-cutoff speech was not pending; returned complete context without waiting")
	}
}
func TestR2AuditRemovedContextsDoNotRetainAllHistoricalState(t *testing.T) {
	m := NewManager(nil)
	for i := 0; i < 40; i++ {
		cfg := managerConfig()
		id := fmt.Sprintf("replaced-%d", i)
		cfg.Pipelines[0].Contexts[0].ID = id
		cfg.Pipelines[0].Sources[0].ContextID = id
		m.Apply(cfg)
		c := cfg.Pipelines[0].Contexts[0]
		g := m.contexts.Generation("p", id)
		m.contexts.Append(c, "p", g, ContextEntry{Kind: "transcript", Text: "retained private content", At: time.Now()})
	}
	m.contexts.mu.RLock()
	defer m.contexts.mu.RUnlock()
	retained := 0
	for _, state := range m.contexts.states {
		retained += len(state.entries)
	}
	if retained > 1 {
		t.Fatalf("removing/replacing context configs retained %d previous context payloads", retained)
	}
}
