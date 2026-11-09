package engine

import (
	"fmt"

	"aioverlay/internal/domain"
	"aioverlay/internal/providers"
)

func (a *modelAttempt) resolveLimits() EffectiveLimits {
	provider, _ := findProvider(a.runner.engine.cfg, a.model.ProviderID)
	limits := ResolveLimits(a.model, a.runner.pipeline.Budget, provider.RequestDefaults)
	a.emit(domain.TraceEvent{Kind: "effective_limits", Message: fmt.Sprintf(
		"connect=%dms; first_token=%dms; response=%dms; idle=%dms; output_tokens=%d",
		limits.ConnectMS, limits.FirstTokenMS, limits.ResponseMS, limits.IdleMS, limits.MaxOutputTokens,
	)})
	return limits
}

func (a *modelAttempt) request(pool domain.ModelPoolConfig, input nodeValue, limits EffectiveLimits) (providers.Request, PromptValues) {
	values := PromptValues{
		InputText: a.runner.input.Text, PreviousOutput: input.text,
		VisionText: input.vision, AudioText: input.transcript, ContextText: input.contextText,
	}
	system, user := RenderRequestPrompt(pool.Prompt, values)
	return providers.Request{
		Audio: input.audio, Task: pool.Task, ProviderID: a.model.ProviderID, ModelID: a.model.ModelID,
		SystemPrompt: system, UserPrompt: user, ImageJPEG: input.image, TextInput: input.text,
		MaxOutputTokens: limits.MaxOutputTokens, ConnectTimeout: durationMS(limits.ConnectMS),
		FirstTokenTimeout: durationMS(limits.FirstTokenMS), ResponseTimeout: durationMS(limits.ResponseMS),
		StreamIdleTimeout: durationMS(limits.IdleMS),
	}, values
}

func (a *modelAttempt) publishPrompt(req providers.Request, values PromptValues) {
	r := a.runner
	if r.opt.OnPrompt == nil {
		return
	}
	snapshot := BoundPromptSnapshot(PromptSnapshot{
		NodeID: r.redact.text(a.node.ID), ProviderID: r.redact.text(a.model.ProviderID),
		ModelID: r.redact.text(a.model.ModelID), ImageAttached: len(req.ImageJPEG) > 0,
		Values: PromptValues{
			InputText: r.redact.text(values.InputText), PreviousOutput: r.redact.text(values.PreviousOutput),
			VisionText: r.redact.text(values.VisionText), AudioText: r.redact.text(values.AudioText),
			ContextText: r.redact.text(values.ContextText),
		},
	})
	r.callbackMu.Lock()
	defer r.callbackMu.Unlock()
	r.opt.OnPrompt(snapshot)
}
