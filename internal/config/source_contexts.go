package config

import (
	"aioverlay/internal/domain"
	"fmt"
	"strings"
)

func validateContexts(p domain.Pipeline) (map[string]domain.ContextConfig, error) {
	contexts := map[string]domain.ContextConfig{}
	for _, c := range p.Contexts {
		if c.ID == "" || len(c.ID) > 120 || strings.ContainsAny(c.ID, "\x00\r\n") {
			return nil, fmt.Errorf("pipeline %s has invalid context id", p.ID)
		}
		if _, dup := contexts[c.ID]; dup {
			return nil, fmt.Errorf("pipeline %s has duplicate context %q", p.ID, c.ID)
		}
		if !within(c.MaxAgeS, 1, 24*3600) || !within(c.MaxEntries, 1, 1000) || !within(c.MaxChars, 100, 2_000_000) || !within(c.ScreenMaxAgeS, 1, 3600) {
			return nil, fmt.Errorf("context %s limits invalid", c.ID)
		}
		contexts[c.ID] = c
	}
	return contexts, nil
}

func (v sourceValidator) entryOK(id string) bool { _, ok := v.nodes[id]; return ok }
func (v sourceValidator) contextOK(id string) bool {
	if id == "" {
		return true
	}
	context, ok := v.contexts[id]
	return ok && context.Enabled
}

func (v sourceValidator) validateNodeContexts(nodes []domain.Node) error {
	for _, node := range nodes {
		if node.ContextMode == "" && node.ContextID == "" {
			continue
		}
		if node.Type != domain.NodeModelPool && node.Type != domain.NodeDecision {
			return fmt.Errorf("node %s can select context only for an AI stage", node.ID)
		}
		mode := node.ContextMode
		if mode == "" && node.ContextID != "" {
			mode = "named"
		}
		if !enum(mode, "trigger", "none", "named") {
			return fmt.Errorf("node %s has invalid context mode", node.ID)
		}
		if mode == "named" {
			if node.ContextID == "" || !v.contextOK(node.ContextID) {
				return fmt.Errorf("node %s references disabled or missing context", node.ID)
			}
		} else if node.ContextID != "" {
			return fmt.Errorf("node %s context id requires named context mode", node.ID)
		}
	}
	return nil
}
