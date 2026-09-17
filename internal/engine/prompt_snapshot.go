package engine

import (
	"strings"
	"unicode/utf8"
)

// Explicit Studio inspection only. This is never placed in trace/logs and carries
// no image bytes. The control layer retains a small bounded last-run window.
type PromptSnapshot struct {
	NodeID        string       `json:"node_id"`
	ProviderID    string       `json:"provider_id"`
	ModelID       string       `json:"model_id"`
	Values        PromptValues `json:"values"`
	ImageAttached bool         `json:"image_attached"`
	Truncated     bool         `json:"truncated"`
}

func BoundPromptSnapshot(in PromptSnapshot) PromptSnapshot {
	remaining := 32 * 1024
	clip := func(s string) string {
		n := len(s)
		if n > remaining {
			n = remaining
			for n > 0 && n < len(s) && !utf8.RuneStart(s[n]) {
				n--
			}
			in.Truncated = true
		}
		remaining -= n
		return strings.Clone(s[:n])
	}
	in.NodeID = clip(in.NodeID)
	in.ProviderID = clip(in.ProviderID)
	in.ModelID = clip(in.ModelID)
	in.Values.InputText = clip(in.Values.InputText)
	in.Values.PreviousOutput = clip(in.Values.PreviousOutput)
	in.Values.VisionText = clip(in.Values.VisionText)
	return in
}
