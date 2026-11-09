package app

import (
	"aioverlay/internal/benchmark"
	"aioverlay/internal/contracts"
	"aioverlay/internal/domain"
	"aioverlay/internal/engine"
	"context"
	"fmt"
	"time"
)

func (a *App) RunBenchmark(ctx context.Context, req contracts.BenchmarkRequest) (contracts.BenchmarkReport, error) {
	return a.RunBenchmarkProgress(ctx, req, nil)
}

// The complete batch owns the config read lease so all rows compare the same
// pipeline, providers and policy state. It never changes the pipeline definition.
func (a *App) RunBenchmarkProgress(ctx context.Context, req contracts.BenchmarkRequest, progress func(contracts.BenchmarkReport)) (contracts.BenchmarkReport, error) {
	if !a.operation.TryRLock() {
		return contracts.BenchmarkReport{}, fmt.Errorf("settings are being saved")
	}
	defer a.operation.RUnlock()
	a.mu.RLock()
	eng := a.engine
	id := req.PipelineID
	if id == "" {
		id = a.cfg.ActivePipelineID
	}
	a.mu.RUnlock()
	img, err := benchmark.ImageJPEG()
	if err != nil {
		return contracts.BenchmarkReport{}, err
	}
	count := req.Runs
	if count <= 0 {
		count = 3
	}
	if count > 10 {
		count = 10
	}
	report := contracts.BenchmarkReport{Requested: count, PipelineID: id, Runs: []contracts.BenchmarkRun{}, CheckMethod: "Exact fixture answer Q4 / 210; other prose is unverified"}
	durations := []int64{}
	for i := 0; i < count; i++ {
		if ctx.Err() != nil {
			report.Cancelled = true
			break
		}
		start := time.Now()
		res, runErr := eng.Execute(ctx, id, engine.Input{ImageJPEG: img}, engine.RunOptions{})
		row := contracts.BenchmarkRun{Run: i + 1, DurationMS: time.Since(start).Milliseconds(), Text: res.Text, Winner: res.Trace.Winner, Verdict: "error"}
		if runErr != nil {
			row.Text = runErr.Error()
		} else {
			report.Successful++
			row.Verdict = benchmark.Evaluate(res.Text)
			row.Correct = row.Verdict == "correct"
			if row.Correct {
				report.Correct++
			}
			if row.Verdict == "unverified" {
				report.Unverified++
			}
		}
		for _, ev := range res.Trace.Events {
			if ev.Kind == "attempt_end" && ev.Outcome == domain.OutcomeSuccess && ev.Attempt != nil && res.Trace.Winner != nil && ev.Attempt.ID == res.Trace.Winner.ID {
				row.TTFTMS = ev.TTFTMS
				row.TPS = ev.TPS
			}
		}
		report.Runs = append(report.Runs, row)
		durations = append(durations, row.DurationMS)
		report.Total = len(report.Runs)
		report.MedianMS = benchmark.Median(durations)
		report.SuccessRate = float64(report.Successful) / float64(report.Total)
		report.Cancelled = ctx.Err() != nil
		if progress != nil {
			progress(contracts.CloneBenchmarkReport(report))
		}
		if report.Cancelled {
			break
		}
	}
	return report, nil
}
