package config

import (
	"aioverlay/internal/domain"
	"fmt"
	"strings"
)

func (v sourceValidator) validateCommands(p domain.Pipeline) (map[string]bool, error) {
	ids := map[string]bool{}
	actions := map[string]bool{}
	for _, c := range p.Commands {
		if c.ID == "" || len(c.ID) > 120 || ids[c.ID] || strings.ContainsAny(c.ID, "\x00\r\n") {
			return nil, fmt.Errorf("invalid or duplicate command ID")
		}
		ids[c.ID] = true
		if !enum(c.Action, "analyze", "screen_context", "screen_decide") {
			return nil, fmt.Errorf("command %s has unsupported action", c.ID)
		}
		if actions[c.Action] {
			return nil, fmt.Errorf("pipeline %s has duplicate command action %q", p.ID, c.Action)
		}
		actions[c.Action] = true
		if err := v.validateCommand(c); err != nil {
			return nil, err
		}
	}
	if _, ok := actions["analyze"]; !ok {
		return nil, fmt.Errorf("pipeline %s requires an analyze command", p.ID)
	}

	return ids, nil
}

func (v sourceValidator) validateCommand(c domain.CommandConfig) error {
	if !enum(c.Intent, domain.IntentRespond, domain.IntentDecide, domain.IntentContext) {
		return fmt.Errorf("command %s has invalid intent", c.ID)
	}
	if !v.entryOK(c.EntryNodeID) {
		return fmt.Errorf("command %s references missing entry", c.ID)
	}
	if !v.contextOK(c.ContextID) {
		return fmt.Errorf("command %s references disabled or missing context", c.ID)
	}
	if !within(c.WaitForTranscriptMS, 0, 10000) {
		return fmt.Errorf("command %s transcript wait is out of range", c.ID)
	}
	if c.Action == "analyze" && !c.CaptureScreen {
		return fmt.Errorf("analyze command must capture the screen")
	}
	if c.Action == "screen_context" {
		ctxCfg, ok := v.contexts[c.ContextID]
		if !ok || !ctxCfg.Enabled || !ctxCfg.KeepLatestScreen {
			return fmt.Errorf("screen_context command %s requires a context that stores the latest screen", c.ID)
		}
	}
	return nil
}
