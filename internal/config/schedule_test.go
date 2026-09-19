package config

import (
	"aioverlay/internal/domain"
	"bytes"
	"encoding/json"
	"testing"
)

func TestSchemaTwoSplitsAllLegacySchedules(t *testing.T) {
	for _, policy := range []domain.PoolPolicy{domain.PoolRoundRobin, domain.PoolPriority, domain.PoolWeighted, domain.PoolFastest, domain.PoolParallel, domain.PoolStaggered} {
		c := Default()
		c.Version = 2
		p := c.Pipelines[0].Nodes[1].ModelPool
		p.Policy = policy
		order, dispatch := p.EffectiveOrder(), p.EffectiveDispatch()
		p.VisionPreprocessor = &domain.ModelPoolConfig{Policy: policy}
		if err := Migrate(&c); err != nil {
			t.Fatal(err)
		}
		if p.Order != order || p.Dispatch != dispatch || p.VisionPreprocessor.Order != order || c.Version != domain.ConfigVersion {
			t.Fatalf("migration changed %s", policy)
		}
	}
}
func TestIndependentScheduleRoundTripAndValidation(t *testing.T) {
	c := Default()
	p := c.Pipelines[0].Nodes[1].ModelPool
	p.Order = domain.PoolRoundRobin
	p.Dispatch = domain.DispatchStaggered
	b, _ := json.Marshal(c)
	out, err := Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	if out.Pipelines[0].Nodes[1].ModelPool.Dispatch != p.Dispatch {
		t.Fatal("lost dispatch")
	}
	p.Order = "parallel_race"
	if Validate(c) == nil {
		t.Fatal("dispatch used as ordering")
	}
	p.Order = domain.PoolPriority
	p.Dispatch = "weighted"
	if Validate(c) == nil {
		t.Fatal("ordering used as dispatch")
	}
}

func TestScreenPolicyAndStageContextRoundTrip(t *testing.T) {
	cfg := Default()
	pipeline := &cfg.Pipelines[0]
	contextCfg := domain.ContextConfig{ID: "screen-context", Name: "Screen", Enabled: true, MaxAgeS: 60, MaxEntries: 10, MaxChars: 1000, KeepLatestScreen: true, ScreenMaxAgeS: 60}
	pipeline.Contexts = append(pipeline.Contexts, contextCfg)
	pipeline.Nodes[1].ContextMode = "named"
	pipeline.Nodes[1].ContextID = contextCfg.ID
	pipeline.Sources = []domain.EventSource{{
		ID: "screen-source", Name: "Screen", Kind: domain.SourceScreenTimer, Enabled: true,
		EntryNodeID: pipeline.EntryNodeID, Intent: domain.IntentRespond, ContextID: contextCfg.ID,
		IntervalMS: 2000, ContextIntervalMS: 4000, TriggerIntervalMS: 8000,
		ChangedOnly: true, DebounceMS: 500, MaxWaitMS: 5000,
		ScreenRegion: &domain.ScreenRegionConfig{X: 10, Y: 20, Width: 640, Height: 480, Extra: map[string]json.RawMessage{"future_region": json.RawMessage(`true`)}},
	}}

	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	source := out.Pipelines[0].Sources[0]
	if source.ContextIntervalMS != 4000 || source.TriggerIntervalMS != 8000 || source.ScreenRegion == nil || source.ScreenRegion.Width != 640 {
		t.Fatalf("screen policy lost: %+v", source)
	}
	if string(source.ScreenRegion.Extra["future_region"]) != "true" {
		t.Fatalf("region extension lost: %+v", source.ScreenRegion.Extra)
	}
	if node := out.Pipelines[0].Nodes[1]; node.ContextMode != "named" || node.ContextID != contextCfg.ID {
		t.Fatalf("stage context policy lost: %+v", node)
	}
}
