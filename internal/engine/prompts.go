package engine

import (
	"aioverlay/internal/domain"
	"strings"
)

type PromptValues struct {
	AudioText      string `json:"audio_text"`
	ContextText    string `json:"context_text"`
	InputText      string `json:"input_text"`
	PreviousOutput string `json:"previous_output"`
	VisionText     string `json:"vision_text"`
}

func RenderPrompt(s string, values PromptValues) string {
	return strings.NewReplacer("{{audio.text}}", values.AudioText, "{{audio.context}}", values.ContextText, "{{context.text}}", values.ContextText, "{{input.text}}", values.InputText, "{{previous.output}}", values.PreviousOutput, "{{vision.text}}", values.VisionText, "{{screen.image}}", "[attached screenshot]").Replace(s)
}

// RenderRequestPrompt is the single user-prompt renderer used by execution and
// Studio preview. Selecting a context is a data policy, not a requirement to
// remember a magic template variable: when {{context.text}} is absent, the
// selected bounded context is attached once as a clearly delimited preamble.
func RenderRequestPrompt(p domain.PromptConfig, values PromptValues) (system, user string) {
	system = RenderPrompt(p.System, values)
	hasContextSlot := strings.Contains(p.User, "{{context.text}}") || strings.Contains(p.User, "{{audio.context}}")
	user = RenderPrompt(p.User, values)
	if values.ContextText != "" && !hasContextSlot {
		user = "Selected context (bounded by the pipeline policy):\n---\n" + values.ContextText + "\n---\n\nCurrent request:\n" + user
	}
	return system, user
}
