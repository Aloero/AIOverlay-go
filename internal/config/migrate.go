package config

import (
	"aioverlay/internal/domain"
	"aioverlay/internal/hotkey"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// Version 2 makes unknown-field round trips explicit. Version 1 data keeps its
// models, transport, keys and enable flags; no model policy is silently changed.
func Migrate(cfg *domain.Config) error {
	for cfg.Version < domain.ConfigVersion {
		switch cfg.Version {
		case 1:
			cfg.Version = 2
		case 2:
			// Split old policies without changing request order or concurrency.
			var migratePool func(*domain.ModelPoolConfig)
			migratePool = func(p *domain.ModelPoolConfig) {
				if p == nil {
					return
				}
				p.Order, p.Dispatch = p.EffectiveOrder(), p.EffectiveDispatch()
				migratePool(p.VisionPreprocessor)
			}
			for i := range cfg.Pipelines {
				for j := range cfg.Pipelines[i].Nodes {
					migratePool(cfg.Pipelines[i].Nodes[j].ModelPool)
				}
			}
			cfg.Version = 3
		case 3:
			// Do not steal a user-assigned combination during upgrade.
			if cfg.Overlay.Hotkeys == nil {
				cfg.Overlay.Hotkeys = map[string]string{}
			}
			if _, exists := cfg.Overlay.Hotkeys["visibility"]; !exists {
				candidate, _ := hotkey.Parse("Ctrl+Shift+H")
				available := true
				for _, text := range cfg.Overlay.Hotkeys {
					b, e := hotkey.Parse(text)
					if e == nil && b == candidate {
						available = false
					}
				}
				cfg.Overlay.Hotkeys["visibility"] = ""
				if available {
					cfg.Overlay.Hotkeys["visibility"] = "Ctrl+Shift+H"
				}
			}
			cfg.Version = 4
		case 4:
			for i := range cfg.Pipelines {
				p := &cfg.Pipelines[i]
				legacyContextID := ""
				manualUsesContext := false
				if p.Context != nil && p.Context.Enabled {
					legacyContextID = "conversation"
					manualUsesContext = p.Context.ManualUsesContext
					p.Contexts = append(p.Contexts, domain.ContextConfig{ID: legacyContextID, Name: "Разговор", Enabled: true, MaxAgeS: p.Context.MaxAgeS, MaxEntries: p.Context.MaxEntries, MaxChars: p.Context.MaxChars, IncludeTranscript: true, IncludeAnswers: p.Context.IncludeAnswers, KeepLatestScreen: p.Context.KeepLatestScreen, ScreenMaxAgeS: p.Context.ScreenMaxAgeS})
				}
				var sources []domain.EventSource
				for _, src := range p.Sources {
					if src.Command != "" || src.Kind == "manual_screen" {
						action := src.Command
						if action == "" {
							action = "analyze"
						}
						ctxID := src.ContextID
						if ctxID == "" && manualUsesContext {
							ctxID = legacyContextID
						}
						p.Commands = append(p.Commands, domain.CommandConfig{ID: src.ID, Name: src.Name, Action: action, EntryNodeID: src.EntryNodeID, Intent: domain.CommandIntent(src.Intent), ContextID: ctxID, CaptureScreen: true})
						continue
					}
					if src.ContextID == "" && legacyContextID != "" {
						src.ContextID = legacyContextID
					}
					sources = append(sources, src)
				}
				p.Sources = sources
				if len(p.Commands) == 0 {
					ctxID := ""
					if manualUsesContext {
						ctxID = legacyContextID
					}
					p.Commands = []domain.CommandConfig{{ID: "screen-answer", Name: "Снимок и ответ", Action: "analyze", EntryNodeID: p.EntryNodeID, Intent: domain.IntentRespond, ContextID: ctxID, CaptureScreen: true}}
				}
				if p.SessionBudget != nil {
					if p.SessionBudget.MaxParallelCalls == 0 {
						p.SessionBudget.MaxParallelCalls = p.Budget.MaxParallelCalls
					}
					if p.SessionBudget.EndToEndMS == 0 {
						p.SessionBudget.EndToEndMS = p.Budget.MaxWallTimeMS
					}
				}
				p.Context = nil
			}
			cfg.Version = 5
		case 5:
			// Schema 6 adds the explicit exit hotkey. As with visibility, a
			// combination already used by another action is never stolen.
			if cfg.Overlay.Hotkeys == nil {
				cfg.Overlay.Hotkeys = map[string]string{}
			}
			if _, exists := cfg.Overlay.Hotkeys["quit"]; !exists {
				candidate, _ := hotkey.Parse("Ctrl+Shift+Q")
				available := true
				for _, text := range cfg.Overlay.Hotkeys {
					b, e := hotkey.Parse(text)
					if e == nil && b == candidate {
						available = false
					}
				}
				cfg.Overlay.Hotkeys["quit"] = ""
				if available {
					cfg.Overlay.Hotkeys["quit"] = "Ctrl+Shift+Q"
				}
			}
			cfg.Version = 6
		case 6:
			// Schema 7 names the delivery application explicitly, so the UI can present
			// "provider → app → registration" without re-deriving it from protocol kinds.
			// Every existing connection keeps its behavior: opencode connections become app
			// "opencode", everything else is "direct".
			for i := range cfg.Providers {
				if cfg.Providers[i].App == "" {
					cfg.Providers[i].App = cfg.Providers[i].DeliveryApp()
				}
			}
			cfg.Version = 7
		default:
			return fmt.Errorf("unsupported old config schema %d", cfg.Version)
		}
	}
	if cfg.Version > domain.ConfigVersion {
		return fmt.Errorf("config schema %d requires a newer application; file was not changed", cfg.Version)
	}
	return nil
}
func checkJSON(raw []byte) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var value func(int) error
	value = func(depth int) error {
		if depth > 64 {
			return fmt.Errorf("JSON nesting exceeds 64")
		}
		tok, err := d.Token()
		if err != nil {
			return fmt.Errorf("invalid JSON")
		}
		delim, ok := tok.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				t, err := d.Token()
				if err != nil {
					return fmt.Errorf("invalid JSON key")
				}
				key, ok := t.(string)
				if !ok || seen[key] {
					return fmt.Errorf("duplicate JSON field")
				}
				seen[key] = true
				if err = value(depth + 1); err != nil {
					return err
				}
			}
		case '[':
			for d.More() {
				if err := value(depth + 1); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("unexpected JSON delimiter")
		}
		_, err = d.Token()
		return err
	}
	if err := value(0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return fmt.Errorf("trailing JSON data")
	}
	return nil
}

// CheckJSON is shared by application import and headless request size boundaries.
func CheckJSON(raw []byte) error { return checkJSON(raw) }
