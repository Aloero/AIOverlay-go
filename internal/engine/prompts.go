package engine

import "strings"

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
