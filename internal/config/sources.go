package config

import (
	"aioverlay/internal/domain"
	"fmt"
	"math"
	"strings"
)

// sourceValidator resolves references without owning runtime or capture state.
type sourceValidator struct {
	nodes     map[string]domain.Node
	contexts  map[string]domain.ContextConfig
	providers map[string]domain.ProviderConfig
}

func validateSources(p domain.Pipeline, nodes map[string]domain.Node, providers map[string]domain.ProviderConfig) error {
	if len(p.Sources) > 16 {
		return fmt.Errorf("pipeline %s has too many background sources", p.ID)
	}
	if len(p.Commands) > 16 {
		return fmt.Errorf("pipeline %s has too many commands", p.ID)
	}
	contexts, err := validateContexts(p)
	if err != nil {
		return err
	}
	validator := sourceValidator{nodes: nodes, contexts: contexts, providers: providers}
	if err := validator.validateNodeContexts(p.Nodes); err != nil {
		return err
	}
	commands, err := validator.validateCommands(p)
	if err != nil {
		return err
	}
	if err := validator.validateEventSources(p.Sources, commands); err != nil {
		return err
	}
	return validateSessionBudget(p.SessionBudget)
}

func validateSessionBudget(b *domain.SessionBudget) error {
	if b != nil {
		if !within(b.CallsPerMinute, 1, 300) || !within(b.QueueCapacity, 1, 64) || !within(b.CooldownMS, 0, 60000) || !within(b.MaxParallelCalls, 1, 64) || !within(b.EndToEndMS, 1000, 600000) {
			return fmt.Errorf("session limits invalid")
		}
	}
	return nil
}

func validWindowsExecutablePath(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" || len(path) > 4096 || strings.ContainsRune(path, 0) {
		return false
	}
	if strings.HasPrefix(path, `\\`) {
		return len(path) > 2
	}
	return len(path) >= 3 && ((path[0] >= 'A' && path[0] <= 'Z') || (path[0] >= 'a' && path[0] <= 'z')) && path[1] == ':' && (path[2] == '\\' || path[2] == '/')
}

func ValidateAudio(a domain.AudioSourceConfig) error {
	if !enum(a.Mode, "system", "microphone", "process") {
		return fmt.Errorf("select system sound, microphone or process-only capture")
	}
	if len(a.DeviceID) > 4096 || strings.ContainsRune(a.DeviceID, 0) {
		return fmt.Errorf("invalid audio device")
	}
	if a.DeviceRole != "" && !enum(a.DeviceRole, "multimedia", "communications") {
		return fmt.Errorf("invalid default audio device role")
	}
	if a.Mode == "process" {
		if a.DeviceID != "" || a.DeviceRole != "" {
			return fmt.Errorf("process-only capture cannot use endpoint device settings")
		}
		if a.Process == nil || !validWindowsExecutablePath(a.Process.ExecutablePath) {
			return fmt.Errorf("process-only capture requires an absolute Windows executable path")
		}
	} else if a.Process != nil {
		return fmt.Errorf("process selection requires process-only capture mode")
	}
	if !within(a.SegmentMS, 1000, 30000) || !within(a.SilenceMS, 150, 5000) || !within(a.MinimumMS, 80, a.SegmentMS) || a.SilenceMS > a.SegmentMS {
		return fmt.Errorf("invalid audio segment/silence limits")
	}
	if math.IsNaN(a.Threshold) || math.IsInf(a.Threshold, 0) || a.Threshold < 0 || a.Threshold > 0.5 {
		return fmt.Errorf("audio threshold out of range")
	}
	return nil
}
