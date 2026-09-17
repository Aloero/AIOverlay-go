package engine

import "strings"

type PromptValues struct {
	InputText      string `json:"input_text"`
	PreviousOutput string `json:"previous_output"`
	VisionText     string `json:"vision_text"`
}

func RenderPrompt(s string, values PromptValues) string {
	return strings.NewReplacer("{{input.text}}", values.InputText, "{{previous.output}}", values.PreviousOutput, "{{vision.text}}", values.VisionText, "{{screen.image}}", "[attached screenshot]").Replace(s)
}
