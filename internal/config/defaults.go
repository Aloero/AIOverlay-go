package config

import "aioverlay/internal/domain"

const (
	DefaultSystemPrompt = `You are a precise screen-task assistant. Answer the actual task visible in the supplied context. Prioritize correctness. Keep the answer concise unless the task requires explanation. Never invent unreadable details.`
	DefaultVisionPrompt = `Read the screenshot and extract the actual task for another reasoning model. Include all answer-relevant text, numbers, choices, labels, units, code, table cells, and diagram relationships. Ignore browser chrome, taskbars, notifications, ads, decorative UI, and previous overlay text. Do not solve the task. If something important is unreadable, mark it as unreadable instead of guessing.`
	DefaultTextPrompt   = `Solve the task below. It was extracted from a screenshot by a vision model. Check constraints, labels, units, choices, code, and numerical details. Return the useful final answer only.\n\n{{vision.text}}`
)

func Default() domain.Config {
	return domain.Config{
		Version: domain.ConfigVersion,
		App: domain.AppConfig{
			Name:                "AIOverlay",
			SecretStorage:       "portable_json",
			PersistRuntimeStats: true,
			CatalogRefreshHours: 24,
		},
		Providers: []domain.ProviderConfig{
			{
				ID:                 "opencode",
				Label:              "Embedded OpenCode",
				Kind:               domain.ProviderEmbeddedOpenCode,
				Enabled:            true,
				OpenCodeProviderID: "opencode",
				CredentialPool:     domain.CredentialPool{Policy: domain.CredentialRoundRobin, MaxKeysPerRequest: 1, RateLimitCooldownS: 60},
				RequestDefaults:    defaultRequestDefaults(),
			},
			{
				ID:              "openrouter",
				Label:           "OpenRouter",
				Kind:            domain.ProviderOpenAICompatible,
				Enabled:         true,
				BaseURL:         "https://openrouter.ai/api/v1",
				ModelsURL:       "https://openrouter.ai/api/v1/models",
				Headers:         map[string]string{"X-Title": "AIOverlay-go"},
				CredentialPool:  domain.CredentialPool{Policy: domain.CredentialRoundRobin, MaxKeysPerRequest: 3, RateLimitCooldownS: 60},
				RequestDefaults: defaultRequestDefaults(),
			},
			{
				ID:              "nvidia",
				Label:           "NVIDIA NIM",
				Kind:            domain.ProviderOpenAICompatible,
				Enabled:         true,
				BaseURL:         "https://integrate.api.nvidia.com/v1",
				ModelsURL:       "https://integrate.api.nvidia.com/v1/models",
				CredentialPool:  domain.CredentialPool{Policy: domain.CredentialRoundRobin, MaxKeysPerRequest: 3, RateLimitCooldownS: 60},
				RequestDefaults: defaultRequestDefaults(),
			},
			{
				ID:              "huggingface",
				Label:           "Hugging Face Router",
				Kind:            domain.ProviderOpenAICompatible,
				Enabled:         true,
				BaseURL:         "https://router.huggingface.co/v1",
				ModelsURL:       "https://router.huggingface.co/v1/models",
				CredentialPool:  domain.CredentialPool{Policy: domain.CredentialRoundRobin, MaxKeysPerRequest: 3, RateLimitCooldownS: 60},
				RequestDefaults: defaultRequestDefaults(),
			},
			{
				ID:              "gemini",
				Label:           "Google Gemini",
				Kind:            domain.ProviderGemini,
				Enabled:         true,
				BaseURL:         "https://generativelanguage.googleapis.com/v1beta",
				CredentialPool:  domain.CredentialPool{Policy: domain.CredentialRoundRobin, MaxKeysPerRequest: 3, RateLimitCooldownS: 60},
				RequestDefaults: defaultRequestDefaults(),
			},
		},
		Pipelines:        []domain.Pipeline{defaultRoundRobinPipeline(), fastStaggeredPipeline(), vlmLLMTemplate()},
		ActivePipelineID: "round-robin-screenshot",
		Overlay: domain.OverlayConfig{
			Opacity:      0.82,
			Width:        640,
			Height:       380,
			FontSize:     14,
			Stealth:      true,
			ClickThrough: true,
			StationaryMS: 0,
			Hotkeys: map[string]string{
				"analyze": "Ctrl+Shift+S",
				"ghost":   "Ctrl+Shift+X",
				"clear":   "Ctrl+Shift+C",
				"control": "Ctrl+Shift+O",
			},
		},
		ControlCenter: domain.ControlConfig{ListenAddress: "127.0.0.1:17891", AutoOpen: false, Theme: "engineering-dark"},
		VPN:           domain.VPNConfig{Enabled: false, Engine: "sing-box"},
		Build: domain.BuildProfile{
			ExecutableName:  "AIOverlay.exe",
			ProductName:     "AIOverlay",
			FileDescription: "AI Overlay Assistant",
			Company:         "Local",
			Version:         "0.2.6",
			IconPath:        "assets/app.ico",
		},
	}
}

func defaultRequestDefaults() domain.RequestDefaults {
	return domain.RequestDefaults{ConnectTimeoutMS: 3000, FirstTokenTimeoutMS: 8000, ResponseTimeoutMS: 15000, StreamIdleTimeoutMS: 5000, MaxOutputTokens: 2048}
}

func strongVisionModels() []domain.ModelSlot {
	return []domain.ModelSlot{
		{ID: "mimo", ProviderID: "opencode", ModelID: "opencode/mimo-v2.5-free", Enabled: true, Priority: 100, Weight: 1.4, QualityScore: 90, TimeoutMS: 14000, FirstTokenTimeoutMS: 8000, MaxAttempts: 1},
		{ID: "muse13", ProviderID: "opencode", ModelID: "opencode/muse-spark-1.3-contributor-free", Enabled: true, Priority: 90, Weight: 1.0, QualityScore: 84, TimeoutMS: 14000, FirstTokenTimeoutMS: 8000, MaxAttempts: 1},
		{ID: "muse12", ProviderID: "opencode", ModelID: "opencode/muse-spark-1.2-contributor-free", Enabled: true, Priority: 85, Weight: 0.8, QualityScore: 82, TimeoutMS: 14000, FirstTokenTimeoutMS: 8000, MaxAttempts: 1},
	}
}

func visionPool(policy domain.PoolPolicy) domain.ModelPoolConfig {
	return domain.ModelPoolConfig{
		Policy: policy, QualityFloor: 80, MaxParallel: 3, MaxCalls: 3, HedgeDelayMS: 2000,
		Prompt: domain.PromptConfig{System: DefaultSystemPrompt, User: `Analyze {{screen.image}} and answer the task directly.`},
		Models: strongVisionModels(),
	}
}

func baseBudget() domain.ExecutionBudget {
	return domain.ExecutionBudget{MaxWallTimeMS: 30000, MaxModelCalls: 8, MaxParallelCalls: 3, MaxProviders: 4, MaxKeysPerProvider: 3, ConnectTimeoutMS: 3000, FirstTokenTimeoutMS: 8000, ResponseTimeoutMS: 15000, StreamIdleTimeoutMS: 5000}
}

func pipelineWithPool(id, name, description string, pool domain.ModelPoolConfig) domain.Pipeline {
	return domain.Pipeline{
		ID: id, Name: name, Description: description, Enabled: true, EntryNodeID: "screen", OutputNodeID: "output", Budget: baseBudget(),
		Nodes: []domain.Node{
			{ID: "screen", Type: domain.NodeScreen, Name: "Screen", Position: domain.Position{X: 80, Y: 180}},
			{ID: "models", Type: domain.NodeModelPool, Name: "Model Pool", Position: domain.Position{X: 360, Y: 140}, ModelPool: &pool},
			{ID: "output", Type: domain.NodeOutput, Name: "Output", Position: domain.Position{X: 760, Y: 180}},
		},
		Edges: []domain.Edge{
			{ID: "e-screen-models", From: "screen", To: "models", On: []domain.Outcome{domain.OutcomeSuccess}, Priority: 100, MaxVisits: 1},
			{ID: "e-models-output", From: "models", To: "output", On: []domain.Outcome{domain.OutcomeSuccess}, Priority: 100, MaxVisits: 1},
		},
	}
}

func defaultRoundRobinPipeline() domain.Pipeline {
	return pipelineWithPool("round-robin-screenshot", "Round Robin Screenshot", "Default: rotate the first model each request, then continue by priority until success.", visionPool(domain.PoolRoundRobin))
}

func fastStaggeredPipeline() domain.Pipeline {
	p := pipelineWithPool("fast-staggered", "Fast Staggered Vision", "MiMo first; launch strong fallbacks after a short hedge delay.", visionPool(domain.PoolStaggered))
	p.Budget.MaxWallTimeMS = 20000
	return p
}

func vlmLLMTemplate() domain.Pipeline {
	llm := domain.ModelPoolConfig{
		Policy: domain.PoolRoundRobin, QualityFloor: 80, MaxParallel: 2, MaxCalls: 3, HedgeDelayMS: 1500,
		Prompt: domain.PromptConfig{System: DefaultSystemPrompt, User: DefaultTextPrompt},
		Models: []domain.ModelSlot{
			{ID: "ling", ProviderID: "opencode", ModelID: "opencode/ling-3.0-flash-fin-free", Enabled: true, Priority: 100, Weight: 1, QualityScore: 86, TimeoutMS: 14000, FirstTokenTimeoutMS: 8000, MaxAttempts: 1},
		},
	}
	vision := visionPool(domain.PoolStaggered)
	vision.Prompt = domain.PromptConfig{System: "You are a precise visual task extractor.", User: DefaultVisionPrompt}
	llm.VisionPreprocessor = &vision
	return pipelineWithPool("vlm-llm-template", "VLM → LLM Template", "Vision pool extracts the task, then a text-only reasoning model solves it.", llm)
}
