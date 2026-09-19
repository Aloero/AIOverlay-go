package app

import (
	"aioverlay/internal/control"
	"aioverlay/internal/domain"
	"aioverlay/internal/engine"
	paudio "aioverlay/internal/platform/audio"
	"aioverlay/internal/providers"
	"aioverlay/internal/session"
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type auditContextProvider struct {
	requests chan providers.Request
	release  chan struct{}
	calls    atomic.Int32
}

func (*auditContextProvider) ID() string { return "opencode" }
func (p *auditContextProvider) Stream(ctx context.Context, r providers.Request, _ string) (<-chan providers.Event, <-chan error) {
	ev := make(chan providers.Event, 2)
	errs := make(chan error, 1)
	n := p.calls.Add(1)
	p.requests <- r
	go func() {
		defer close(ev)
		defer close(errs)
		if n == 1 {
			select {
			case <-p.release:
			case <-ctx.Done():
				errs <- ctx.Err()
				return
			}
		}
		ev <- providers.Event{Type: providers.EventDelta, Text: "ANSWER", ModelID: r.ModelID}
		ev <- providers.Event{Type: providers.EventDone, ModelID: r.ModelID}
	}()
	return ev, errs
}
func TestR2AuditQueuedManualDoesNotSendClearedConversation(t *testing.T) {
	a := studioTestApp(t)
	cfg := a.GetConfig()
	p := &cfg.Pipelines[0]
	c := domain.ContextConfig{ID: "audit-context", Name: "audit", Enabled: true, MaxAgeS: 60, MaxEntries: 10, MaxChars: 2000, IncludeTranscript: true, KeepLatestScreen: true, ScreenMaxAgeS: 60}
	p.Contexts = append(p.Contexts, c)
	p.Commands[0].ContextID = c.ID
	if err := a.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	g := a.sessions.Contexts().Generation(p.ID, c.ID)
	a.sessions.Contexts().Append(c, p.ID, g, session.ContextEntry{ID: "old", Kind: "transcript", Text: "CLEARED_PRIVATE_CONTENT", At: time.Now().Add(-time.Second)})
	fake := &auditContextProvider{requests: make(chan providers.Request, 4), release: make(chan struct{})}
	a.registry.Set("opencode", fake)
	s := control.New("127.0.0.1:0", a)
	s.Capture = func() ([]byte, error) { return []byte("screen"), nil }
	if _, err := s.StartCommand("analyze"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-fake.requests:
	case <-time.After(time.Second):
		t.Fatal("first request not started")
	}
	if _, err := s.StartCommand("analyze"); err != nil {
		close(fake.release)
		t.Fatal(err)
	}
	if _, err := a.ClearConversation(c.ID); err != nil {
		close(fake.release)
		t.Fatal(err)
	}
	close(fake.release)
	select {
	case r := <-fake.requests:
		if strings.Contains(r.UserPrompt+r.SystemPrompt, "CLEARED_PRIVATE_CONTENT") {
			t.Error("queued request dispatched erased conversation after Clear")
		}
	case <-time.After(time.Second):
	}
	deadline := time.Now().Add(time.Second)
	for s.CurrentJob().Running && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
}
func TestR2AuditSourcePermissionRevocationDoesNotWaitForActiveTurn(t *testing.T) {
	a := studioTestApp(t)
	cfg := a.GetConfig()
	p := &cfg.Pipelines[0]
	pool := *p.Nodes[1].ModelPool
	pool.Task = "generate"
	pool.ResultName = "decision"
	p.Nodes = append(p.Nodes, domain.Node{ID: "audit-decision", Name: "decision", Type: domain.NodeDecision, ModelPool: &pool}, domain.Node{ID: "audit-capture", Name: "capture", Type: domain.NodeCapture})
	p.Edges = append(p.Edges, domain.Edge{ID: "audit-need-screen", From: "audit-decision", To: "audit-capture", On: []domain.Outcome{domain.OutcomeNeedScreen}, MaxVisits: 1}, domain.Edge{ID: "audit-capture-answer", From: "audit-capture", To: p.Nodes[1].ID, On: []domain.Outcome{domain.OutcomeSuccess}, MaxVisits: 1})
	p.Sources = []domain.EventSource{{ID: "audit-audio", Kind: domain.SourceAudio, Name: "audit", Enabled: true, AutoStart: true, Intent: domain.IntentDecide, EntryNodeID: "audit-decision", Audio: &domain.AudioSourceConfig{Mode: "system", SegmentMS: 1000, MinimumMS: 80, SilenceMS: 200, Threshold: .01}}}
	if err := a.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	{
		release := make(chan struct{})
		entered := make(chan struct{})
		calls := 0
		m := session.NewManager(func(ctx context.Context, _ string, _ engine.Input, opt engine.RunOptions) (engine.Result, error) {
			close(entered)
			<-release
			if opt.Capture != nil {
				_, _ = opt.Capture(ctx)
			}
			return engine.Result{}, nil
		})
		a.sessions = m
		m.SetCapture(nil, func(context.Context) ([]byte, error) { calls++; return []byte("frame"), nil })
		m.SetAudioCapture(func(ctx context.Context, _ domain.AudioSourceConfig, emit func(paudio.Block)) error {
			samples := make([]int16, 16000)
			for i := range samples {
				samples[i] = 12000
			}
			emit(paudio.Block{Samples: samples, Rate: 16000, At: time.Now()})
			<-ctx.Done()
			return ctx.Err()
		})
		m.Start(t.Context(), cfg)
		defer m.Close()
		<-entered
		revoked := a.GetConfig()
		revoked.Pipelines[0].Sources[0].Enabled = false
		saved := make(chan error, 1)
		go func() { saved <- a.SaveConfig(revoked) }()
		select {
		case err := <-saved:
			close(release)
			if err != nil {
				t.Fatal(err)
			}
			return
		case <-time.After(50 * time.Millisecond):
		}
		blocked := len(saved) == 0
		close(release)
		err := <-saved
		if err != nil {
			t.Fatal(err)
		}
		if blocked && calls != 0 {
			t.Fatalf("permission save waited for model turn; old turn performed %d new capture after revocation was requested", calls)
		}
	}
}
