package engine

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"time"

	"aioverlay/internal/domain"
)

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
