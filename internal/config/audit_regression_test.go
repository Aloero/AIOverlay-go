package config

import (
	"aioverlay/internal/domain"
	"bytes"
	"encoding/json"
	"testing"
)

func TestAuditRejectUnsafeInvalidConfig(t *testing.T) {
	cases := map[string]func(*domain.Config){
		"duplicate_semantic_hotkey": func(c *domain.Config) {
			c.Overlay.Hotkeys["analyze"] = "Ctrl+Shift+S"
			c.Overlay.Hotkeys["control"] = "Shift+Control+S"
		},
		"invalid_hotkey":           func(c *domain.Config) { c.Overlay.Hotkeys["control"] = "garbage" },
		"missing_recovery_hotkey":  func(c *domain.Config) { delete(c.Overlay.Hotkeys, "control") },
		"unknown_node_type":        func(c *domain.Config) { c.Pipelines[0].Nodes[0].Type = "typo" },
		"unknown_outcome":          func(c *domain.Config) { c.Pipelines[0].Edges[0].On = []domain.Outcome{"typo"} },
		"unknown_pool_policy":      func(c *domain.Config) { c.Pipelines[0].Nodes[1].ModelPool.Policy = "typo" },
		"disconnected_output":      func(c *domain.Config) { c.Pipelines[0].Edges = nil },
		"disabled_active_pipeline": func(c *domain.Config) { c.Pipelines[0].Enabled = false },
		"negative_limits":          func(c *domain.Config) { c.Pipelines[0].Budget.MaxKeysPerProvider = -1 },
		"duplicate_credential_secret": func(c *domain.Config) {
			c.Providers[1].CredentialPool.Keys = []domain.Credential{{ID: "a", Secret: "same", Enabled: true}, {ID: "b", Secret: "same", Enabled: true}}
		},
		"invalid_validator_regex": func(c *domain.Config) {
			c.Pipelines[0].Nodes = append(c.Pipelines[0].Nodes, domain.Node{ID: "check", Type: domain.NodeValidator, Validator: &domain.ValidatorConfig{Pattern: "["}})
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			c := Default()
			change(&c)
			if err := Validate(c); err == nil {
				t.Fatal("invalid configuration accepted")
			}
		})
	}
}
func TestAuditUnknownFieldRoundTrip(t *testing.T) {
	raw, _ := json.Marshal(Default())
	var c map[string]any
	_ = json.Unmarshal(raw, &c)
	c["future_setting"] = map[string]any{"value": 1}
	raw, _ = json.Marshal(c)
	if _, err := Decode(bytes.NewReader(raw)); err != nil {
		t.Fatalf("portable forward field rejected instead of preserved: %v", err)
	}
}
