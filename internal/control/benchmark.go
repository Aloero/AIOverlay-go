package control

import (
	"aioverlay/internal/contracts"
	"aioverlay/internal/engine"
	"context"
)

type benchmarkProgressBackend interface {
	RunBenchmarkProgress(context.Context, BenchmarkRequest, func(BenchmarkReport)) (BenchmarkReport, error)
}

// CloneBenchmarkReport is retained for existing control callers.
func CloneBenchmarkReport(b BenchmarkReport) BenchmarkReport {
	return contracts.CloneBenchmarkReport(b)
}

func (s *Server) runBenchmark(ctx context.Context, req BenchmarkRequest, opt engine.RunOptions) (engine.Result, *BenchmarkReport, error) {
	var report BenchmarkReport
	var err error
	if b, ok := s.backend.(benchmarkProgressBackend); ok {
		report, err = b.RunBenchmarkProgress(ctx, req, func(report BenchmarkReport) {
			s.jobs.mu.Lock()
			defer s.jobs.mu.Unlock()
			if s.jobs.job.Running && !s.jobs.job.Cleared {
				copy := CloneBenchmarkReport(report)
				s.jobs.job.Benchmark = &copy
				s.jobs.job.Phase = "benchmark"
			}
		})
	} else {
		report, err = s.backend.RunBenchmark(ctx, req)
	}
	return engine.Result{}, &report, err
}
