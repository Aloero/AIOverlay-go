package app

import (
	"aioverlay/internal/control"
	"aioverlay/internal/domain"
	"aioverlay/internal/engine"
	"aioverlay/internal/providers"
	"context"
	"testing"
)

type benchmarkProvider struct{}

func (benchmarkProvider) ID() string { return "opencode" }
func (benchmarkProvider) Stream(ctx context.Context, r providers.Request, key string) (<-chan providers.Event, <-chan error) {
	ev := make(chan providers.Event, 3)
	errs := make(chan error)
	ev <- providers.Event{Type: providers.EventFirstToken}
	ev <- providers.Event{Type: providers.EventDelta, Text: "Q4, 210"}
	ev <- providers.Event{Type: providers.EventDone, OutputTokens: 5}
	close(ev)
	close(errs)
	return ev, errs
}
func TestBenchmarkBatchGradesAndCountsOnlyAttemptedRuns(t *testing.T) {
	a := studioTestApp(t)
	a.registry.Set("opencode", benchmarkProvider{})
	// The fixture still travels through the real engine and its active configuration.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	report, err := a.RunBenchmarkProgress(ctx, control.BenchmarkRequest{Runs: 5}, func(p control.BenchmarkReport) {
		if p.Total == 2 {
			cancel()
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Total != 2 || report.Requested != 5 || !report.Cancelled || report.Correct != 2 || report.Successful != 2 || report.SuccessRate != 1 {
		t.Fatalf("unexpected report %+v", report)
	}
	for _, r := range report.Runs {
		if r.Verdict != "correct" || r.Winner == nil {
			t.Fatal("winner/verdict missing")
		}
	}
}
func TestStatisticsRoundTripDoesNotOverwriteQuality(t *testing.T) {
	a := studioTestApp(t)
	a.registry.Set("opencode", benchmarkProvider{})
	cfg := a.GetConfig()
	score := cfg.Pipelines[0].Nodes[1].ModelPool.Models[0].QualityScore
	if _, err := a.Run(context.Background(), cfg.ActivePipelineID, engine.Input{ImageJPEG: []byte("test")}, engine.RunOptions{}); err != nil {
		t.Fatal(err)
	}
	stats := a.engine.ExportStatistics()
	if len(stats.Models) == 0 {
		t.Fatal("no stats")
	}
	cfg.App.PersistRuntimeStats = true
	if err := a.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := a.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.RuntimeStatistics == nil || len(loaded.RuntimeStatistics.Models) == 0 || loaded.Pipelines[0].Nodes[1].ModelPool.Models[0].QualityScore != score {
		t.Fatal("stats lost or changed manual quality")
	}
	_ = domain.OutcomeSuccess
}
