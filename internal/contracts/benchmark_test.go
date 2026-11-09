package contracts

import (
	"aioverlay/internal/domain"
	"testing"
)

func TestBenchmarkCloneOwnsRowsAndWinner(t *testing.T) {
	original := BenchmarkReport{Runs: []BenchmarkRun{{Text: "original", Winner: &domain.AttemptRef{ModelID: "saved-model"}}, {Text: "without winner"}}}
	copy := CloneBenchmarkReport(original)
	copy.Runs[0].Text = "changed"
	copy.Runs[0].Winner.ModelID = "changed-model"
	copy.Runs[1].Text = "changed"
	if original.Runs[0].Text != "original" || original.Runs[0].Winner.ModelID != "saved-model" || original.Runs[1].Text != "without winner" {
		t.Fatal("progress consumer mutated the original report")
	}
	if copy.Runs[1].Winner != nil {
		t.Fatal("clone fabricated a winner")
	}
	if CloneBenchmarkReport(BenchmarkReport{}).Runs != nil {
		t.Fatal("nil report rows changed representation")
	}
}
