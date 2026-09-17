package config

import (
	"aioverlay/internal/domain"
	"testing"
)

func TestScreenSourceDebounceValidation(t *testing.T) {
	validSource := domain.EventSource{
		ID:          "screen-watch",
		Name:        "Screen watch",
		Kind:        domain.SourceScreenTimer,
		Enabled:     true,
		EntryNodeID: Default().Pipelines[0].EntryNodeID,
		Intent:      domain.IntentRespond,
		IntervalMS:  1000,
		ChangedOnly: true,
		DebounceMS:  500,
		MaxWaitMS:   2000,
	}

	t.Run("valid", func(t *testing.T) {
		cfg := Default()
		source := validSource
		source.EntryNodeID = cfg.Pipelines[0].EntryNodeID
		cfg.Pipelines[0].Sources = append(cfg.Pipelines[0].Sources, source)
		if err := Validate(cfg); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("debounce_exceeds_max_wait", func(t *testing.T) {
		cfg := Default()
		source := validSource
		source.EntryNodeID = cfg.Pipelines[0].EntryNodeID
		source.DebounceMS = 3000
		source.MaxWaitMS = 2000
		cfg.Pipelines[0].Sources = append(cfg.Pipelines[0].Sources, source)
		if err := Validate(cfg); err == nil {
			t.Fatal("invalid screen debounce accepted")
		}
	})
}
