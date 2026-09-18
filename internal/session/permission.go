package session

import (
	"aioverlay/internal/domain"
	"context"
	"reflect"
)

type capturePermissionPolicy struct {
	PipelineID string
	Commands   []commandCapturePermission
	Sources    []sourceCapturePermission
	Actions    []string
}

type commandCapturePermission struct {
	ID            string
	Action        string
	EntryNodeID   string
	CaptureScreen bool
}

type sourceCapturePermission struct {
	ID         string
	Kind       domain.SourceKind
	Enabled    bool
	AutoStart  bool
	AudioMode  string
	DeviceID   string
	DeviceRole string
}

func sameCapturePermissions(left, right domain.Config) bool {
	return reflect.DeepEqual(capturePermissions(left), capturePermissions(right))
}

func capturePermissions(cfg domain.Config) capturePermissionPolicy {
	pipeline := activePipeline(cfg)
	if pipeline == nil {
		return capturePermissionPolicy{}
	}
	policy := capturePermissionPolicy{PipelineID: pipeline.ID}
	for _, command := range pipeline.Commands {
		policy.Commands = append(policy.Commands, commandCapturePermission{
			ID: command.ID, Action: command.Action, EntryNodeID: command.EntryNodeID, CaptureScreen: command.CaptureScreen,
		})
	}
	for _, source := range pipeline.Sources {
		permission := sourceCapturePermission{ID: source.ID, Kind: source.Kind, Enabled: source.Enabled, AutoStart: source.AutoStart}
		if source.Audio != nil {
			permission.AudioMode = source.Audio.Mode
			permission.DeviceID = source.Audio.DeviceID
			permission.DeviceRole = source.Audio.DeviceRole
		}
		policy.Sources = append(policy.Sources, permission)
	}
	for _, node := range pipeline.Nodes {
		if node.Type == domain.NodeCapture {
			policy.Actions = append(policy.Actions, node.ID)
		}
	}
	return policy
}

func (m *Manager) permissionedCapture(generation uint64, capture ScreenCapture) ScreenCapture {
	if capture == nil {
		return nil
	}
	return func(ctx context.Context) ([]byte, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !m.permissionCurrent(generation) {
			return nil, errCapturePermissionChanged
		}
		image, err := capture(ctx)
		if err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !m.permissionCurrent(generation) {
			return nil, errCapturePermissionChanged
		}
		return image, nil
	}
}

func (m *Manager) permissionCurrent(generation uint64) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return generation != 0 && generation == m.permissionGeneration
}
