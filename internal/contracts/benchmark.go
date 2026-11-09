package contracts

// CloneBenchmarkReport isolates progress consumers from the live report.
func CloneBenchmarkReport(b BenchmarkReport) BenchmarkReport {
	b.Runs = append([]BenchmarkRun(nil), b.Runs...)
	for i := range b.Runs {
		if b.Runs[i].Winner != nil {
			a := *b.Runs[i].Winner
			b.Runs[i].Winner = &a
		}
	}
	return b
}
