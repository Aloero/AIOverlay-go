package config

import (
	"aioverlay/internal/domain"
	"fmt"
	"math"
	"strings"
)

func validateSources(p domain.Pipeline, nodes map[string]domain.Node) error {
	if len(p.Sources) > 16 {
		return fmt.Errorf("pipeline %s has too many background sources", p.ID)
	}
	if len(p.Commands) > 16 {
		return fmt.Errorf("pipeline %s has too many commands", p.ID)
	}
	contexts := map[string]domain.ContextConfig{}
	for _, c := range p.Contexts {
		if c.ID == "" || len(c.ID) > 120 || strings.ContainsAny(c.ID, "\x00\r\n") {
			return fmt.Errorf("pipeline %s has invalid context id", p.ID)
		}
		if _, dup := contexts[c.ID]; dup {
			return fmt.Errorf("pipeline %s has duplicate context %q", p.ID, c.ID)
		}
		if !within(c.MaxAgeS, 1, 24*3600) || !within(c.MaxEntries, 1, 1000) || !within(c.MaxChars, 100, 2_000_000) || !within(c.ScreenMaxAgeS, 1, 3600) {
			return fmt.Errorf("context %s limits invalid", c.ID)
		}
		contexts[c.ID] = c
	}
	entryOK := func(id string) bool { _, ok := nodes[id]; return ok }
	contextOK := func(id string) bool {
		if id == "" {
			return true
		}
		c, ok := contexts[id]
		return ok && c.Enabled
	}
	for _, node := range p.Nodes {
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
			if node.ContextID == "" || !contextOK(node.ContextID) {
				return fmt.Errorf("node %s references disabled or missing context", node.ID)
			}
		} else if node.ContextID != "" {
			return fmt.Errorf("node %s context id requires named context mode", node.ID)
		}
	}
	ids := map[string]bool{}
	actions := map[string]bool{}
	for _, c := range p.Commands {
		if c.ID == "" || len(c.ID) > 120 || ids[c.ID] || strings.ContainsAny(c.ID, "\x00\r\n") {
			return fmt.Errorf("invalid or duplicate command ID")
		}
		ids[c.ID] = true
		if !enum(c.Action, "analyze", "screen_context", "screen_decide") {
			return fmt.Errorf("command %s has unsupported action", c.ID)
		}
		if actions[c.Action] {
			return fmt.Errorf("pipeline %s has duplicate command action %q", p.ID, c.Action)
		}
		actions[c.Action] = true
		if !enum(c.Intent, domain.IntentRespond, domain.IntentDecide, domain.IntentContext) {
			return fmt.Errorf("command %s has invalid intent", c.ID)
		}
		if !entryOK(c.EntryNodeID) {
			return fmt.Errorf("command %s references missing entry", c.ID)
		}
		if !contextOK(c.ContextID) {
			return fmt.Errorf("command %s references disabled or missing context", c.ID)
		}
		if !within(c.WaitForTranscriptMS, 0, 10000) {
			return fmt.Errorf("command %s transcript wait is out of range", c.ID)
		}
		if c.Action == "analyze" && !c.CaptureScreen {
			return fmt.Errorf("analyze command must capture the screen")
		}
		if c.Action == "screen_context" {
			ctxCfg, ok := contexts[c.ContextID]
			if !ok || !ctxCfg.Enabled || !ctxCfg.KeepLatestScreen {
				return fmt.Errorf("screen_context command %s requires a context that stores the latest screen", c.ID)
			}
		}
	}
	if _, ok := actions["analyze"]; !ok {
		return fmt.Errorf("pipeline %s requires an analyze command", p.ID)
	}

	sourceIDs := map[string]bool{}
	for _, s := range p.Sources {
		if s.ID == "" || len(s.ID) > 120 || sourceIDs[s.ID] || ids[s.ID] || strings.ContainsAny(s.ID, "\x00\r\n") {
			return fmt.Errorf("invalid, duplicate or ambiguous source ID")
		}
		sourceIDs[s.ID] = true
		if !enum(s.Kind, domain.SourceAudio, domain.SourceScreenTimer) {
			return fmt.Errorf("unsupported source kind")
		}
		if !enum(s.Intent, domain.IntentRespond, domain.IntentDecide, domain.IntentContext) {
			return fmt.Errorf("source intent must be respond, decide or context")
		}
		if !entryOK(s.EntryNodeID) {
			return fmt.Errorf("source %s references missing entry", s.ID)
		}
		if !contextOK(s.ContextID) {
			return fmt.Errorf("source %s references disabled or missing context", s.ID)
		}
		if s.Command != "" {
			return fmt.Errorf("schema-5 background source cannot own a hotkey command")
		}
		if s.Kind == domain.SourceScreenTimer {
			if !within(s.IntervalMS, 1000, 600000) {
				return fmt.Errorf("screen source interval must be 1..600 seconds")
			}
			if s.ContextIntervalMS != 0 && !within(s.ContextIntervalMS, 1000, 600000) {
				return fmt.Errorf("screen context interval must be 1..600 seconds or 0 to inherit capture interval")
			}
			if s.TriggerIntervalMS != 0 && !within(s.TriggerIntervalMS, 1000, 600000) {
				return fmt.Errorf("screen AI trigger interval must be 1..600 seconds or 0 to inherit capture interval")
			}
			if region := s.ScreenRegion; region != nil {
				if region.X < 0 || region.Y < 0 || !within(region.Width, 1, 16384) || !within(region.Height, 1, 16384) {
					return fmt.Errorf("screen region must use non-negative origin and 1..16384 dimensions")
				}
			}
			if !within(s.DebounceMS, 0, 60000) {
				return fmt.Errorf("screen source debounce must be 0..60 seconds")
			}
			if !within(s.MaxWaitMS, 0, 600000) {
				return fmt.Errorf("screen source maximum wait must be 0..600 seconds")
			}
			if s.MaxWaitMS > 0 && s.DebounceMS > s.MaxWaitMS {
				return fmt.Errorf("screen source debounce cannot exceed maximum wait")
			}
			if s.Audio != nil {
				return fmt.Errorf("screen source cannot contain audio settings")
			}
		}
		if s.Kind == domain.SourceAudio {
			if s.Audio == nil {
				return fmt.Errorf("audio source missing settings")
			}
			if err := ValidateAudio(*s.Audio); err != nil {
				return fmt.Errorf("source %s: %w", s.ID, err)
			}
		}
		if s.Intent == domain.IntentContext && s.ContextID == "" {
			return fmt.Errorf("context-only source requires a context")
		}
	}
	if b := p.SessionBudget; b != nil {
		if !within(b.CallsPerMinute, 1, 300) || !within(b.QueueCapacity, 1, 64) || !within(b.CooldownMS, 0, 60000) || !within(b.MaxParallelCalls, 1, 64) || !within(b.EndToEndMS, 1000, 600000) {
			return fmt.Errorf("session limits invalid")
		}
	}
	return nil
}

func ValidateAudio(a domain.AudioSourceConfig) error {
	if !enum(a.Mode, "system", "microphone") {
		return fmt.Errorf("select system sound or microphone; process-only capture is not implemented")
	}
	if len(a.DeviceID) > 4096 || strings.ContainsRune(a.DeviceID, 0) {
		return fmt.Errorf("invalid audio device")
	}
	if a.DeviceRole != "" && !enum(a.DeviceRole, "multimedia", "communications") {
		return fmt.Errorf("invalid default audio device role")
	}
	if !within(a.SegmentMS, 1000, 30000) || !within(a.SilenceMS, 150, 5000) || !within(a.MinimumMS, 80, a.SegmentMS) || a.SilenceMS > a.SegmentMS {
		return fmt.Errorf("invalid audio segment/silence limits")
	}
	if math.IsNaN(a.Threshold) || math.IsInf(a.Threshold, 0) || a.Threshold < 0 || a.Threshold > 0.5 {
		return fmt.Errorf("audio threshold out of range")
	}
	return nil
}
