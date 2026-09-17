package engine

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"time"

	"aioverlay/internal/domain"
)

func enabledModels(all []domain.ModelSlot, floor float64) []domain.ModelSlot {
	var out []domain.ModelSlot
	for _, m := range all {
		if !m.Enabled {
			continue
		}
		if m.QualityScore < floor {
			continue
		}
		out = append(out, m)
	}
	return out
}
func runValidator(v *domain.ValidatorConfig, text string) (domain.Outcome, error) {
	if v == nil {
		return domain.OutcomeSuccess, nil
	}
	if v.MinLength > 0 && len(text) < v.MinLength {
		return domain.OutcomeValidationFailed, fmt.Errorf("response shorter than %d", v.MinLength)
	}
	if v.MaxLength > 0 && len(text) > v.MaxLength {
		return domain.OutcomeValidationFailed, fmt.Errorf("response longer than %d", v.MaxLength)
	}
	if v.Pattern != "" {
		re, err := regexp.Compile(v.Pattern)
		if err != nil {
			return domain.OutcomeInvalidResponse, err
		}
		if !re.MatchString(text) {
			return domain.OutcomeValidationFailed, fmt.Errorf("response does not match validator")
		}
	}
	return domain.OutcomeSuccess, nil
}
func renderPrompt(s, input string) string {
	s = strings.ReplaceAll(s, "{{vision.text}}", input)
	s = strings.ReplaceAll(s, "{{previous.output}}", input)
	s = strings.ReplaceAll(s, "{{input.text}}", input)
	s = strings.ReplaceAll(s, "{{screen.image}}", "[attached screenshot]")
	return s
}
func findPipeline(cfg domain.Config, id string) (domain.Pipeline, bool) {
	if id == "" {
		id = cfg.ActivePipelineID
	}
	for _, p := range cfg.Pipelines {
		if p.ID == id && p.Enabled {
			return p, true
		}
	}
	return domain.Pipeline{}, false
}
func findProvider(cfg domain.Config, id string) (domain.ProviderConfig, bool) {
	for _, p := range cfg.Providers {
		if p.ID == id {
			return p, true
		}
	}
	return domain.ProviderConfig{}, false
}
func findNode(p domain.Pipeline, id string) (domain.Node, bool) {
	for _, n := range p.Nodes {
		if n.ID == id {
			return n, true
		}
	}
	return domain.Node{}, false
}
func pcMaxOutput(cfg domain.Config, id string) int {
	if p, ok := findProvider(cfg, id); ok {
		return p.RequestDefaults.MaxOutputTokens
	}
	return 0
}
func durationMS(v int) time.Duration {
	if v <= 0 {
		return 0
	}
	return time.Duration(v) * time.Millisecond
}
func firstPositive(vs ...int) int {
	for _, v := range vs {
		if v > 0 {
			return v
		}
	}
	return 0
}
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
func newID(prefix string) string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return prefix + "_" + hex.EncodeToString(b)
}
