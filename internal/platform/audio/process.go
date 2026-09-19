package audio

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

const minimumProcessLoopbackBuild = 20348

type Capabilities struct {
	SystemLoopback              bool   `json:"system_loopback"`
	Microphone                  bool   `json:"microphone"`
	ProcessLoopback             bool   `json:"process_loopback"`
	ProcessLoopbackReason       string `json:"process_loopback_reason,omitempty"`
	MinimumProcessLoopbackBuild int    `json:"minimum_process_loopback_build,omitempty"`
}

type Process struct {
	PID       uint32    `json:"pid"`
	ParentPID uint32    `json:"parent_pid,omitempty"`
	Name      string    `json:"name"`
	Path      string    `json:"path"`
	StartedAt time.Time `json:"started_at"`
}

type ProcessCaptureErrorKind string

const (
	ProcessCaptureUnsupported ProcessCaptureErrorKind = "process_unsupported"
	ProcessCaptureUnavailable ProcessCaptureErrorKind = "process_unavailable"
	ProcessCaptureAmbiguous   ProcessCaptureErrorKind = "process_ambiguous"
	ProcessCaptureChanged     ProcessCaptureErrorKind = "process_changed"
)

type ProcessCaptureError struct {
	Kind    ProcessCaptureErrorKind
	Message string
}

func (err *ProcessCaptureError) Error() string {
	if err == nil {
		return "process capture error"
	}
	if strings.TrimSpace(err.Message) != "" {
		return err.Message
	}
	return string(err.Kind)
}

func RecoverableProcessTarget(err error) bool {
	var processErr *ProcessCaptureError
	if !errors.As(err, &processErr) {
		return false
	}
	return processErr.Kind == ProcessCaptureUnavailable || processErr.Kind == ProcessCaptureChanged
}

type processIdentity struct {
	PID       uint32
	Path      string
	StartedAt time.Time
}

func selectProcessIdentity(executablePath string, processes []Process) (processIdentity, error) {
	wanted := normalizeProcessPath(executablePath)
	if wanted == "" {
		return processIdentity{}, &ProcessCaptureError{Kind: ProcessCaptureUnavailable, Message: "process-only capture requires an executable path"}
	}
	matches := make([]Process, 0, 1)
	for _, process := range processes {
		if normalizeProcessPath(process.Path) == wanted {
			matches = append(matches, process)
		}
	}
	if len(matches) == 0 {
		return processIdentity{}, &ProcessCaptureError{Kind: ProcessCaptureUnavailable, Message: fmt.Sprintf("process-only target is not running: %s", executablePath)}
	}
	match := uniqueProcessTreeRoot(matches)
	if match == nil {
		return processIdentity{}, &ProcessCaptureError{Kind: ProcessCaptureAmbiguous, Message: fmt.Sprintf("multiple independent process trees match %s; process-only capture will not guess", executablePath)}
	}
	return processIdentity{PID: match.PID, Path: match.Path, StartedAt: match.StartedAt}, nil
}

func uniqueProcessTreeRoot(matches []Process) *Process {
	ids := make(map[uint32]struct{}, len(matches))
	for _, process := range matches {
		ids[process.PID] = struct{}{}
	}
	var root *Process
	for index := range matches {
		process := &matches[index]
		if _, parentMatches := ids[process.ParentPID]; parentMatches {
			continue
		}
		if root != nil {
			return nil
		}
		root = process
	}
	return root
}

func sameProcessIdentity(expected, current processIdentity) bool {
	return expected.PID != 0 && expected.PID == current.PID &&
		normalizeProcessPath(expected.Path) == normalizeProcessPath(current.Path) &&
		!expected.StartedAt.IsZero() && expected.StartedAt.Equal(current.StartedAt)
}

func normalizeProcessPath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.ReplaceAll(path, "/", `\`)
	for strings.Contains(path, `\\`) {
		path = strings.ReplaceAll(path, `\\`, `\`)
	}
	path = strings.TrimRight(path, `\`)
	return strings.ToLower(path)
}
