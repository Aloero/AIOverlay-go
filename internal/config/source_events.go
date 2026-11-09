package config

import (
	"aioverlay/internal/domain"
	"fmt"
	"strings"
)

func (v sourceValidator) validateEventSources(sources []domain.EventSource, commandIDs map[string]bool) error {
	sourceIDs := map[string]bool{}
	for _, s := range sources {
		if s.ID == "" || len(s.ID) > 120 || sourceIDs[s.ID] || commandIDs[s.ID] || strings.ContainsAny(s.ID, "\x00\r\n") {
			return fmt.Errorf("invalid, duplicate or ambiguous source ID")
		}
		sourceIDs[s.ID] = true
		if !enum(s.Kind, domain.SourceAudio, domain.SourceScreenTimer) {
			return fmt.Errorf("unsupported source kind")
		}
		if !enum(s.Intent, domain.IntentRespond, domain.IntentDecide, domain.IntentContext) {
			return fmt.Errorf("source intent must be respond, decide or context")
		}
		if s.Realtime == nil && !v.entryOK(s.EntryNodeID) {
			return fmt.Errorf("source %s references missing entry", s.ID)
		}
		if !v.contextOK(s.ContextID) {
			return fmt.Errorf("source %s references disabled or missing context", s.ID)
		}
		if s.Command != "" {
			return fmt.Errorf("schema-5 background source cannot own a hotkey command")
		}
		if s.Realtime != nil {
			if err := v.validateRealtimeSource(s); err != nil {
				return err
			}
		}
		if s.Kind == domain.SourceScreenTimer {
			if err := v.validateScreenSource(s); err != nil {
				return err
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
		if s.Intent == domain.IntentContext && s.ContextID == "" && s.Realtime == nil {
			return fmt.Errorf("context-only source requires a context")
		}
	}
	return nil
}

func (v sourceValidator) validateRealtimeSource(s domain.EventSource) error {
	if s.Kind != domain.SourceAudio {
		return fmt.Errorf("source %s realtime session requires an audio source", s.ID)
	}
	provider, ok := v.providers[s.Realtime.ProviderID]
	if !ok || !provider.Enabled || provider.Kind != domain.ProviderOpenAILive {
		return fmt.Errorf("source %s realtime provider is missing, disabled or not openai_live", s.ID)
	}
	if strings.TrimSpace(s.Realtime.ModelID) == "" || len(s.Realtime.ModelID) > 2048 || strings.ContainsRune(s.Realtime.ModelID, 0) {
		return fmt.Errorf("source %s realtime model is invalid", s.ID)
	}
	if !enum(s.Realtime.ResponsePolicy, "manual", "each_segment") {
		return fmt.Errorf("source %s realtime response policy is invalid", s.ID)
	}
	if s.Intent == domain.IntentDecide {
		return fmt.Errorf("source %s realtime route cannot claim semantic no_action", s.ID)
	}
	if s.Realtime.ResponsePolicy == "manual" && s.Intent != domain.IntentContext {
		return fmt.Errorf("source %s manual realtime policy must use context intent", s.ID)
	}
	if s.Realtime.ResponsePolicy == "each_segment" && s.Intent != domain.IntentRespond {
		return fmt.Errorf("source %s automatic realtime policy must use respond intent", s.ID)
	}
	if s.Realtime.ResponsePolicy == "each_segment" && (provider.Realtime == nil || strings.TrimSpace(provider.Realtime.ResponsesModel) == "") {
		return fmt.Errorf("source %s automatic realtime response requires Responses delegation", s.ID)
	}
	return nil
}

func (v sourceValidator) validateScreenSource(s domain.EventSource) error {
	if !within(s.IntervalMS, 1000, 600000) {
		return fmt.Errorf("screen source interval must be 1..600 seconds")
	}
	if s.ContextIntervalMS != 0 && !within(s.ContextIntervalMS, 1000, 600000) {
		return fmt.Errorf("screen context interval must be 1..600 seconds or 0 to inherit capture interval")
	}
	if s.TriggerIntervalMS != 0 && !within(s.TriggerIntervalMS, 1000, 600000) {
		return fmt.Errorf("screen AI trigger interval must be 1..600 seconds or 0 to inherit capture interval")
	}
	if s.ContextIntervalMS > 0 && s.ContextIntervalMS < s.IntervalMS {
		return fmt.Errorf("screen context interval cannot be faster than local capture interval")
	}
	if s.TriggerIntervalMS > 0 && s.TriggerIntervalMS < s.IntervalMS {
		return fmt.Errorf("screen AI trigger interval cannot be faster than local capture interval")
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
	return nil
}
