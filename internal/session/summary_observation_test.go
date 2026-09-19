package session

import (
	"testing"
	"time"

	"aioverlay/internal/engine"
)

func TestManualSummaryObservationStoresPinnedGenerationProvenance(t *testing.T) {
	manager := NewManager(nil)
	cfg := managerConfig()
	manager.cfg = cfg
	contextCfg := cfg.Pipelines[0].Contexts[0]
	generation := manager.contexts.Generation("p", contextCfg.ID)
	prepared := PreparedCommand{PipelineID: "p", Command: cfg.Pipelines[0].Commands[0], Context: contextCfg, ContextGeneration: generation, ContextGenerations: map[string]uint64{contextCfg.ID: generation}}
	observation := engine.Observation{Kind: "context.summary", Text: "summary", ContextID: contextCfg.ID, SegmentID: "summary-1", DerivedFrom: []string{"entry-1"}, Revision: 1, Final: true}
	if !manager.RecordCommandObservation(prepared, observation) {
		t.Fatal("manual summary observation was not stored")
	}
	contextCfg.IncludeSummaries = true
	snapshot := manager.contexts.Snapshot(contextCfg, "p", time.Now().Add(time.Second))
	if len(snapshot.Entries) != 1 || snapshot.Entries[0].DerivedFrom[0] != "entry-1" {
		t.Fatalf("stored summary=%+v", snapshot.Entries)
	}
}
