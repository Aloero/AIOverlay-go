package providers

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"aioverlay/internal/domain"
)

const localSTTProtocol = "aio-stt-v1"

// LocalSTT is a keyless transcription provider backed by one long-lived helper.
// The helper receives raw PCM16 over stdin/stdout IPC; media is never written to disk.
type LocalSTT struct {
	cfg domain.ProviderConfig

	mu               sync.Mutex
	runtime          *LocalSTTRuntime
	activeRequests   int
	backgroundPaused bool
	unloadPending    bool
}

func NewLocalSTT(cfg domain.ProviderConfig) *LocalSTT { return &LocalSTT{cfg: cfg} }

func (provider *LocalSTT) ID() string        { return provider.cfg.ID }
func (provider *LocalSTT) Incremental() bool { return false }

func (provider *LocalSTT) Stream(ctx context.Context, request Request, _ string) (<-chan Event, <-chan error) {
	events := make(chan Event, 4)
	errors := make(chan error, 1)
	go func() {
		defer close(events)
		defer close(errors)
		if err := provider.transcribe(ctx, request, events); err != nil {
			errors <- err
		}
	}()
	return events, errors
}

func (provider *LocalSTT) transcribe(ctx context.Context, request Request, events chan<- Event) error {
	if err := validateAudioRequest(provider.cfg, request); err != nil {
		return err
	}
	if provider.cfg.LocalSTT == nil {
		return &RequestError{Outcome: domain.OutcomeProviderUnavailable, Message: "Local STT settings are missing"}
	}
	if request.ModelID != provider.cfg.LocalSTT.ModelID {
		return &RequestError{Outcome: domain.OutcomeModelUnavailable, Message: "Requested local STT model does not match the configured resource"}
	}
	runtime, err := provider.acquireRuntime()
	if err != nil {
		return err
	}
	defer provider.releaseRuntime()
	text, err := runtime.Transcribe(ctx, *request.Audio)
	if err != nil {
		return localSTTRequestError(err)
	}
	if !sendEvent(ctx, events, Event{Type: EventDelta, ModelID: request.ModelID, Text: text}) {
		return ctx.Err()
	}
	if !sendEvent(ctx, events, Event{Type: EventDone, ModelID: request.ModelID}) {
		return ctx.Err()
	}
	return nil
}

func (provider *LocalSTT) acquireRuntime() (*LocalSTTRuntime, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if provider.runtime == nil || !provider.runtime.Alive() {
		if provider.runtime != nil {
			provider.runtime.Close()
			provider.runtime = nil
		}
		command, err := provider.helperCommand()
		if err != nil {
			return nil, err
		}
		runtime, err := StartLocalSTTRuntime(command)
		if err != nil {
			return nil, &RequestError{Outcome: domain.OutcomeProviderUnavailable, Message: err.Error(), Retryable: true}
		}
		provider.runtime = runtime
	}
	provider.activeRequests++
	if provider.backgroundPaused && localSTTResourcePolicy(provider.cfg) == "unload_on_pause" {
		provider.unloadPending = true
	}
	return provider.runtime, nil
}

func (provider *LocalSTT) releaseRuntime() {
	var unload *LocalSTTRuntime
	provider.mu.Lock()
	if provider.activeRequests > 0 {
		provider.activeRequests--
	}
	if provider.activeRequests == 0 && provider.unloadPending {
		unload = provider.runtime
		provider.runtime = nil
		provider.unloadPending = false
	}
	provider.mu.Unlock()
	if unload != nil {
		unload.Close()
	}
}

func (provider *LocalSTT) SetBackgroundPaused(paused bool) {
	var unload *LocalSTTRuntime
	provider.mu.Lock()
	provider.backgroundPaused = paused
	if localSTTResourcePolicy(provider.cfg) == "unload_on_pause" && paused {
		if provider.activeRequests > 0 {
			provider.unloadPending = true
		} else {
			unload = provider.runtime
			provider.runtime = nil
			provider.unloadPending = false
		}
	} else if !paused {
		provider.unloadPending = false
	}
	provider.mu.Unlock()
	if unload != nil {
		unload.Close()
	}
}

func localSTTResourcePolicy(cfg domain.ProviderConfig) string {
	if cfg.LocalSTT == nil || cfg.LocalSTT.ResourcePolicy == "" {
		return "keep_warm"
	}
	return cfg.LocalSTT.ResourcePolicy
}

func (provider *LocalSTT) helperCommand() (*exec.Cmd, error) {
	cfg := provider.cfg.LocalSTT
	if cfg == nil {
		return nil, &RequestError{Outcome: domain.OutcomeProviderUnavailable, Message: "Local STT settings are missing"}
	}
	helperPath, err := resolveLocalSTTResource(cfg.HelperPath, "helper")
	if err != nil {
		return nil, err
	}
	modelPath, err := resolveLocalSTTResource(cfg.ModelPath, "model")
	if err != nil {
		return nil, err
	}
	command := exec.Command(helperPath, append([]string(nil), cfg.Args...)...)
	configureHiddenChild(command)
	command.Dir = filepath.Dir(helperPath)
	command.Env = append(sanitizedEnv(),
		"AIO_STT_PROTOCOL="+localSTTProtocol,
		"AIO_STT_MODEL="+modelPath,
		"AIO_STT_MODEL_ID="+cfg.ModelID,
	)
	command.Stderr = io.Discard
	return command, nil
}

func resolveLocalSTTResource(configured, kind string) (string, error) {
	configured = strings.TrimSpace(configured)
	if configured == "" {
		return "", &RequestError{Outcome: domain.OutcomeProviderUnavailable, Message: "Local STT " + kind + " path is empty"}
	}
	path := filepath.Clean(configured)
	if !filepath.IsAbs(path) {
		executable, err := os.Executable()
		if err != nil {
			return "", &RequestError{Outcome: domain.OutcomeProviderUnavailable, Message: "Cannot resolve AIOverlay executable directory"}
		}
		path = filepath.Join(filepath.Dir(executable), path)
	}
	info, err := os.Stat(path)
	if err != nil {
		outcome := domain.OutcomeProviderUnavailable
		if kind == "model" {
			outcome = domain.OutcomeModelUnavailable
		}
		return "", &RequestError{Outcome: outcome, Message: fmt.Sprintf("Local STT %s is unavailable at the configured path", kind)}
	}
	if info.IsDir() || info.Size() == 0 {
		outcome := domain.OutcomeProviderUnavailable
		if kind == "model" {
			outcome = domain.OutcomeModelUnavailable
		}
		return "", &RequestError{Outcome: outcome, Message: fmt.Sprintf("Local STT %s resource is invalid", kind)}
	}
	return path, nil
}

func localSTTRequestError(err error) error {
	if err == nil {
		return nil
	}
	outcome := OutcomeOf(err)
	if outcome == domain.OutcomeInvalidResponse {
		outcome = domain.OutcomeProviderUnavailable
	}
	return &RequestError{Outcome: outcome, Message: err.Error(), Retryable: outcome == domain.OutcomeProviderUnavailable}
}

func (provider *LocalSTT) Close() {
	provider.mu.Lock()
	runtime := provider.runtime
	provider.runtime = nil
	provider.unloadPending = false
	provider.mu.Unlock()
	if runtime != nil {
		runtime.Close()
	}
}

type LocalSTTResourceStatus struct {
	Configured       bool   `json:"configured"`
	HelperPresent    bool   `json:"helper_present"`
	ModelPresent     bool   `json:"model_present"`
	ModelID          string `json:"model_id,omitempty"`
	ModelBytes       int64  `json:"model_bytes,omitempty"`
	ResourcePolicy   string `json:"resource_policy,omitempty"`
	Warm             bool   `json:"warm"`
	ActiveRequests   int    `json:"active_requests,omitempty"`
	BackgroundPaused bool   `json:"background_paused,omitempty"`
	Reason           string `json:"reason,omitempty"`
}

func (provider *LocalSTT) LocalSTTStatus() LocalSTTResourceStatus {
	status := localSTTResourceStatus(provider.cfg)
	provider.mu.Lock()
	status.Warm = provider.runtime != nil && provider.runtime.Alive()
	status.ActiveRequests = provider.activeRequests
	status.BackgroundPaused = provider.backgroundPaused
	provider.mu.Unlock()
	return status
}

func localSTTResourceStatus(cfg domain.ProviderConfig) LocalSTTResourceStatus {
	status := LocalSTTResourceStatus{}
	if cfg.LocalSTT == nil {
		status.Reason = "Local STT settings are missing"
		return status
	}
	status.Configured = true
	status.ModelID = cfg.LocalSTT.ModelID
	status.ResourcePolicy = localSTTResourcePolicy(cfg)
	helperPath, err := resolveLocalSTTResource(cfg.LocalSTT.HelperPath, "helper")
	if err != nil {
		status.Reason = err.Error()
		return status
	}
	status.HelperPresent = true
	_ = helperPath
	modelPath, err := resolveLocalSTTResource(cfg.LocalSTT.ModelPath, "model")
	if err != nil {
		status.Reason = err.Error()
		return status
	}
	status.ModelPresent = true
	if info, statErr := os.Stat(modelPath); statErr == nil {
		status.ModelBytes = info.Size()
	}
	return status
}
