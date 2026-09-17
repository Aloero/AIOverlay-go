package config

import (
	"aioverlay/internal/domain"
	"fmt"
	"math"
	"strings"
)

func validateSources(p domain.Pipeline, nodes map[string]domain.Node) error {
	if len(p.Sources) > 8 {
		return fmt.Errorf("at most 8 sources per pipeline")
	}
	seen, commands := map[string]bool{}, map[string]bool{}
	for _, s := range p.Sources {
		if s.ID == "" || len(s.ID) > 120 || seen[s.ID] || strings.ContainsAny(s.ID, "\x00\r\n") {
			return fmt.Errorf("invalid or duplicate source ID")
		}
		seen[s.ID] = true
		if !enum(s.Kind, "manual_screen", "audio", "screen_timer") {
			return fmt.Errorf("unsupported source kind")
		}
		if !enum(s.Intent, "respond", "decide", "context") {
			return fmt.Errorf("source intent must be respond, decide or context")
		}
		if _, ok := nodes[s.EntryNodeID]; !ok {
			return fmt.Errorf("source %s references missing entry", s.ID)
		}
		if s.Kind == "manual_screen" {
			if !enum(s.Command, "analyze", "screen_context", "screen_decide") {
				return fmt.Errorf("unknown manual source command")
			}
			if s.Enabled && commands[s.Command] {
				return fmt.Errorf("duplicate manual command binding")
			}
			if s.Enabled {
				commands[s.Command] = true
			}
		} else if s.Command != "" {
			return fmt.Errorf("only manual sources may have a hotkey command")
		}
		if s.Kind == "screen_timer" && !within(s.IntervalMS, 1000, 600000) {
			return fmt.Errorf("screen source interval must be 1..600 seconds")
		}
		if s.Kind == "audio" {
			if s.Audio == nil {
				return fmt.Errorf("audio source missing settings")
			}
			if err := ValidateAudio(*s.Audio); err != nil {
				return err
			}
		}
		if s.Intent == "context" && (p.Context == nil || !p.Context.Enabled) {
			return fmt.Errorf("context-only source requires an enabled pipeline context")
		}
	}
	if c := p.Context; c != nil {
		if !within(c.MaxAgeS, 1, 3600) || !within(c.MaxEntries, 1, 100) || !within(c.MaxChars, 100, 100000) || !within(c.ScreenMaxAgeS, 1, 600) {
			return fmt.Errorf("context limits invalid")
		}
	}
	if b := p.SessionBudget; b != nil {
		if !within(b.CallsPerMinute, 1, 300) || !within(b.QueueCapacity, 1, 16) || !within(b.CooldownMS, 0, 60000) {
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
	if !within(a.SegmentMS, 1000, 30000) || !within(a.SilenceMS, 200, 3000) || !within(a.MinimumMS, 100, a.SegmentMS) || a.SilenceMS > a.SegmentMS {
		return fmt.Errorf("invalid audio segment/silence limits")
	}
	if math.IsNaN(a.Threshold) || math.IsInf(a.Threshold, 0) || a.Threshold < 0 || a.Threshold > 0.5 {
		return fmt.Errorf("audio threshold out of range")
	}
	return nil
}
