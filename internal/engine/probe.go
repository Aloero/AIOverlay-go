package engine

import (
	"aioverlay/internal/domain"
	"context"
	"fmt"
)

type ConnectionProbe struct {
	ProviderID   string `json:"provider_id"`
	ModelID      string `json:"model_id"`
	CredentialID string `json:"credential_id"`
	Confirm      bool   `json:"confirm"`
}

// Exactly one explicitly chosen saved model/key, no image, retries or fallback.
// Reuses transports without reconfiguring the overlay's active pipeline.
func (e *Engine) Probe(ctx context.Context, req ConnectionProbe, opt RunOptions) (Result, error) {
	if !req.Confirm || req.ModelID == "" || len(req.ModelID) > 2048 {
		return Result{}, fmt.Errorf("explicit model test confirmation required")
	}
	e.configMu.RLock()
	defer e.configMu.RUnlock()
	pc, ok := findProvider(e.cfg, req.ProviderID)
	if !ok || !pc.Enabled {
		return Result{}, fmt.Errorf("saved connection is missing or disabled")
	}
	switch pc.Kind {
	case domain.ProviderLocalSTT:
		return Result{}, fmt.Errorf("local STT must be tested through the audio transcription preview")
	case domain.ProviderOpenAILive:
		return Result{}, fmt.Errorf("OpenAI Live must be tested through a persistent Live source")
	}
	var chosen domain.Credential
	found := false
	for _, k := range pc.CredentialPool.Keys {
		if k.ID == req.CredentialID && k.Enabled && k.Secret != "" {
			chosen = k
			found = true
			break
		}
	}
	anonymous := pc.Kind == domain.ProviderEmbeddedOpenCode && (pc.OpenCodeProviderID == "" || pc.OpenCodeProviderID == "opencode") && req.CredentialID == "keyless"
	if !found && !anonymous {
		return Result{}, fmt.Errorf("choose an enabled saved key")
	}
	if anonymous {
		if e.catalog == nil {
			return Result{}, fmt.Errorf("keyless model is not configured")
		}
		ready := false
		for _, route := range e.catalog.Routes(e.cfg) {
			if route.ConfigID == pc.ID && route.ModelID == req.ModelID && route.Ready && route.Model.Free {
				ready = true
				break
			}
		}
		if !ready {
			return Result{}, fmt.Errorf("model is not configured for keyless access")
		}
	}
	cfg := e.cfg
	pc.CredentialPool.Keys = nil
	if found {
		pc.CredentialPool.Keys = []domain.Credential{chosen}
	}
	pc.CredentialPool.MaxKeysPerRequest = 1
	pc.RequestDefaults.MaxOutputTokens = 32
	cfg.Providers = []domain.ProviderConfig{pc}
	p := domain.Pipeline{ID: "connection-test", Name: "Проверка подключения", Enabled: true, EntryNodeID: "check", OutputNodeID: "output",
		Budget: domain.ExecutionBudget{MaxWallTimeMS: 12000, MaxModelCalls: 1, MaxParallelCalls: 1, MaxProviders: 1, MaxKeysPerProvider: 1, ConnectTimeoutMS: 3000, FirstTokenTimeoutMS: 6000, ResponseTimeoutMS: 10000, StreamIdleTimeoutMS: 3000},
		Nodes:  []domain.Node{{ID: "check", Type: domain.NodeModelPool, Name: "Проверка", ModelPool: &domain.ModelPoolConfig{Policy: domain.PoolPriority, Order: domain.PoolPriority, Dispatch: domain.DispatchSequential, MaxCalls: 1, MaxParallel: 1, Prompt: domain.PromptConfig{User: "Reply only OK."}, Models: []domain.ModelSlot{{ID: "check-model", ProviderID: pc.ID, ModelID: req.ModelID, Enabled: true, MaxAttempts: 1, QualityScore: 100}}}}, {ID: "output", Type: domain.NodeOutput}},
		Edges:  []domain.Edge{{ID: "done", From: "check", To: "output", On: []domain.Outcome{domain.OutcomeSuccess}, MaxVisits: 1}}}
	cfg.Pipelines = []domain.Pipeline{p}
	cfg.ActivePipelineID = p.ID
	isolated := New(cfg, e.registry, e.catalog, nil)
	isolated.revision = e.revision
	opt.Purpose = "connection_test"
	result, err := isolated.Execute(ctx, p.ID, Input{Text: "Reply only OK."}, opt)
	for _, ev := range result.Trace.Events {
		if ev.Kind == "attempt_end" && ev.Attempt != nil && found {
			e.markCredential(pc.ID, chosen, ev.Outcome, pc.CredentialPool.RateLimitCooldownS)
		}
	}
	e.traces.Add(result.Trace)
	return result, err
}
