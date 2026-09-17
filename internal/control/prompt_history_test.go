package control

import (
	"aioverlay/internal/engine"
	"encoding/json"
	"strings"
	"testing"
)

func TestPromptHistoryExplicitBoundedAndCleared(t *testing.T) {
	j := &JobManager{job: Job{ID: "run-test"}}
	for i := 0; i < 40; i++ {
		j.rememberPrompt("run-test", engine.PromptSnapshot{NodeID: string(rune('A' + i)), Values: engine.PromptValues{InputText: strings.Repeat("я", 50000)}})
	}
	last := j.LastPrompts()
	if len(last.Items) != 16 {
		t.Fatal(len(last.Items))
	}
	for _, p := range last.Items {
		if !p.Truncated || len(p.Values.InputText) > 32*1024 {
			t.Fatal("unbounded prompt")
		}
	}
	raw, _ := json.Marshal(j.Snapshot())
	if strings.Contains(string(raw), "яя") {
		t.Fatal("prompt data escaped into automatic job polling")
	}
	j.Clear()
	if len(j.LastPrompts().Items) != 0 {
		t.Fatal("clear retained prompt data")
	}
	j.rememberPrompt("run-test", engine.PromptSnapshot{Values: engine.PromptValues{InputText: "late"}})
	if len(j.LastPrompts().Items) > 0 {
		t.Fatal("late callback restored cleared data")
	}
}
