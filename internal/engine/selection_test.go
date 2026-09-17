package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"aioverlay/internal/catalog"
	"aioverlay/internal/config"
	"aioverlay/internal/domain"
	"aioverlay/internal/providers"
)

func selectionCatalog(t *testing.T) *catalog.Service {
	t.Helper()
	dir := t.TempDir()
	data := `{"test":{"name":"Test","env":["TEST_API_KEY"],"models":{
 "a-image":{"name":"A","modalities":{"input":["text","image"],"output":["text"]},"cost":{"input":0,"output":0}},
 "b-image":{"name":"B","modalities":{"input":["text","image"],"output":["text"]},"cost":{"input":0,"output":0}},
 "c-text":{"name":"C","modalities":{"input":["text"],"output":["text"]},"cost":{"input":0,"output":0}},
 "d-paid":{"name":"D","modalities":{"input":["image"],"output":["text"]},"cost":{"input":2,"output":3}},
 "e-embedding":{"name":"E","modalities":{"input":["text"],"output":["embedding"]},"cost":{"input":0,"output":0}}
 }}}`
	path := filepath.Join(dir, "catalog.json")
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	cat, err := catalog.New(path)
	if err != nil {
		t.Fatal(err)
	}
	return cat
}
func selectionConfig() domain.Config {
	c := config.Default()
	c.Providers = []domain.ProviderConfig{{ID: "test", Kind: domain.ProviderOpenAICompatible, Enabled: true, BaseURL: "http://example.invalid", CredentialPool: domain.CredentialPool{Policy: domain.CredentialSequential, MaxKeysPerRequest: 2, Keys: []domain.Credential{{ID: "one", Label: "Key alias", Secret: "never-print-this-key", Enabled: true}}}}}
	p := c.Pipelines[0]
	c.Pipelines = []domain.Pipeline{p}
	pool := c.Pipelines[0].Nodes[1].ModelPool
	pool.Models = nil
	pool.Policy = domain.PoolPriority
	pool.QualityFloor = 0
	pool.MaxCalls = 8
	pool.Selection = &domain.CatalogSelection{Mode: "catalog", Modality: "auto", FreeOnly: true}
	return c
}
func TestCatalogPoolRunsWithoutPinnedModels(t *testing.T) {
	cat, cfg := selectionCatalog(t), selectionConfig()
	var calls atomic.Int32
	script := &scriptedProvider{run: func(ctx context.Context, r providers.Request, key string, ch chan<- providers.Event) error {
		calls.Add(1)
		if r.ModelID == "a-image" {
			return &providers.RequestError{Outcome: domain.OutcomeModelUnavailable, Message: "not offered"}
		}
		if r.ModelID != "b-image" || string(r.ImageJPEG) != "original" {
			return fmt.Errorf("wrong request")
		}
		ch <- providers.Event{Type: providers.EventDelta, Text: "answer"}
		return nil
	}}
	reg, _ := providers.NewRegistry(cfg)
	reg.Set("test", script)
	t.Cleanup(reg.Close)
	eng := New(cfg, reg, cat, nil)
	res, err := eng.Execute(context.Background(), cfg.ActivePipelineID, Input{ImageJPEG: []byte("original")}, RunOptions{})
	if err != nil || res.Text != "answer" || calls.Load() != 2 {
		t.Fatalf("result=%q calls=%d error=%v", res.Text, calls.Load(), err)
	}
	starts := 0
	planSeen := false
	for _, e := range res.Trace.Events {
		if e.Kind == "attempt_start" {
			starts++
			if e.Attempt.CredentialAlias != "Key alias" {
				t.Fatal("missing credential alias")
			}
		}
		if e.Kind == "pool_plan" {
			planSeen = true
		}
	}
	if starts != 2 || !planSeen {
		t.Fatal("trace omitted actual attempts or plan")
	}
	raw, _ := json.Marshal(res.Trace)
	if strings.Contains(string(raw), "never-print-this-key") {
		t.Fatal("secret in trace")
	}
}
func TestCatalogPlanFiltersRoutesCostsModalitiesAndQuality(t *testing.T) {
	cat, cfg := selectionCatalog(t), selectionConfig()
	pool := *cfg.Pipelines[0].Nodes[1].ModelPool
	plan := PlanPool(cfg, cat, pool, true)
	if len(plan.Candidates) != 2 || plan.Skipped["needs_vision"] != 2 || plan.Skipped["not_free"] != 1 {
		t.Fatalf("%+v", plan)
	}
	pool.QualityFloor = 80
	if got := PlanPool(cfg, cat, pool, true); len(got.Candidates) != 0 || got.Skipped["quality_floor"] != 2 {
		t.Fatal("unscored models bypassed quality floor")
	}
	pool.QualityFloor = 0
	pool.Selection.FreeOnly = false
	if got := PlanPool(cfg, cat, pool, true); len(got.Candidates) != 3 {
		t.Fatal("paid opt-in missing")
	}
	cfg.Providers[0].Enabled = false
	if got := PlanPool(cfg, cat, pool, true); len(got.Candidates) != 0 {
		t.Fatal("disabled provider became configured")
	}
}
func TestCatalogOverridesPriorityDisabledAndDedup(t *testing.T) {
	cat, cfg := selectionCatalog(t), selectionConfig()
	pool := *cfg.Pipelines[0].Nodes[1].ModelPool
	pool.Models = []domain.ModelSlot{{ID: "pinned", ProviderID: "test", ModelID: "b-image", Enabled: true, Priority: 900, QualityScore: 95}, {ID: "excluded", ProviderID: "test", ModelID: "a-image", Enabled: false}}
	plan := PlanPool(cfg, cat, pool, true)
	if len(plan.Candidates) != 1 || plan.Candidates[0].ID != "pinned" || plan.Candidates[0].QualityScore != 95 {
		t.Fatalf("%+v", plan)
	}
}
func TestCatalogActualCallBudgetStopsBeforeNextModel(t *testing.T) {
	cat, cfg := selectionCatalog(t), selectionConfig()
	cfg.Pipelines[0].Budget.MaxModelCalls = 1
	var calls atomic.Int32
	script := &scriptedProvider{run: func(ctx context.Context, r providers.Request, key string, ch chan<- providers.Event) error {
		calls.Add(1)
		return &providers.RequestError{Outcome: domain.OutcomeModelUnavailable, Message: "down"}
	}}
	reg, _ := providers.NewRegistry(cfg)
	reg.Set("test", script)
	t.Cleanup(reg.Close)
	_, err := New(cfg, reg, cat, nil).Execute(context.Background(), cfg.ActivePipelineID, Input{ImageJPEG: []byte("frame")}, RunOptions{})
	if err == nil || calls.Load() != 1 {
		t.Fatalf("calls=%d error=%v", calls.Load(), err)
	}
}
func TestFallbackCatalogNeverRotatesAheadOfPinnedTier(t *testing.T) {
	cfg := selectionConfig()
	pool := *cfg.Pipelines[0].Nodes[1].ModelPool
	pool.Selection.Mode = "manual_then_catalog"
	pool.Policy = domain.PoolRoundRobin
	pool.Models = []domain.ModelSlot{{ID: "pin", ProviderID: "test", ModelID: "b-image", Enabled: true, Priority: 100, QualityScore: 90}}
	plan := PlanPool(cfg, selectionCatalog(t), pool, true)
	eng := New(cfg, nil, nil, nil)
	for i := 0; i < 6; i++ {
		ordered := eng.orderPoolModels("x", pool, plan.Candidates)
		if ordered[0].ID != "pin" {
			t.Fatal("catalog rotated ahead of selected list")
		}
	}
}
func TestCatalogTextFilterRequiresVisionAndAcceptsExtractedText(t *testing.T) {
	cfg, cat := selectionConfig(), selectionCatalog(t)
	pool := *cfg.Pipelines[0].Nodes[1].ModelPool
	pool.Selection.Modality = "text"
	if got := PlanPool(cfg, cat, pool, true); len(got.Candidates) != 0 {
		t.Fatal("image could be sent to text-only source")
	}
	got := PlanPool(cfg, cat, pool, false)
	if len(got.Candidates) != 1 || got.Candidates[0].ModelID != "c-text" {
		t.Fatalf("%+v", got)
	}
}

func TestCatalogParallelRespectsGlobalConcurrencyAndCallBudget(t *testing.T) {
	for _, policy := range []domain.PoolPolicy{domain.PoolParallel, domain.PoolStaggered} {
		t.Run(string(policy), func(t *testing.T) {
			cat, cfg := selectionCatalog(t), selectionConfig()
			pool := cfg.Pipelines[0].Nodes[1].ModelPool
			pool.Policy = policy
			pool.MaxParallel = 8
			pool.HedgeDelayMS = 2
			cfg.Pipelines[0].Budget.MaxParallelCalls = 1
			cfg.Pipelines[0].Budget.MaxModelCalls = 1
			var calls, active, peak atomic.Int32
			script := &scriptedProvider{run: func(ctx context.Context, r providers.Request, key string, ch chan<- providers.Event) error {
				calls.Add(1)
				now := active.Add(1)
				defer active.Add(-1)
				for old := peak.Load(); now > old; old = peak.Load() {
					if peak.CompareAndSwap(old, now) {
						break
					}
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(8 * time.Millisecond):
				}
				ch <- providers.Event{Type: providers.EventDelta, Text: "winner"}
				return nil
			}}
			reg, _ := providers.NewRegistry(cfg)
			reg.Set("test", script)
			t.Cleanup(reg.Close)
			res, err := New(cfg, reg, cat, nil).Execute(context.Background(), cfg.ActivePipelineID, Input{ImageJPEG: []byte("frame")}, RunOptions{})
			if err != nil || res.Text != "winner" || calls.Load() != 1 || peak.Load() != 1 {
				t.Fatalf("text=%q calls=%d peak=%d error=%v", res.Text, calls.Load(), peak.Load(), err)
			}
		})
	}
}

func TestExhaustedRaceBudgetDoesNotCancelAnAdmittedWinner(t *testing.T) {
	cat, cfg := selectionCatalog(t), selectionConfig()
	pool := cfg.Pipelines[0].Nodes[1].ModelPool
	pool.Policy = domain.PoolParallel
	pool.MaxParallel = 3
	cfg.Pipelines[0].Budget.MaxParallelCalls = 3
	cfg.Pipelines[0].Budget.MaxModelCalls = 1
	var calls atomic.Int32
	script := &scriptedProvider{run: func(ctx context.Context, r providers.Request, key string, ch chan<- providers.Event) error {
		calls.Add(1)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
		ch <- providers.Event{Type: providers.EventDelta, Text: "admitted winner"}
		return nil
	}}
	reg, _ := providers.NewRegistry(cfg)
	reg.Set("test", script)
	t.Cleanup(reg.Close)
	res, err := New(cfg, reg, cat, nil).Execute(context.Background(), cfg.ActivePipelineID, Input{ImageJPEG: []byte("frame")}, RunOptions{})
	if err != nil || res.Text != "admitted winner" || calls.Load() != 1 {
		t.Fatalf("text=%q calls=%d error=%v", res.Text, calls.Load(), err)
	}
}
