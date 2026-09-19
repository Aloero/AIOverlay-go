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
	ID                     string
	Kind                   domain.SourceKind
	Enabled                bool
	AutoStart              bool
	AudioMode              string
	DeviceID               string
	DeviceRole             string
	ContextID              string
	RealtimeProviderID     string
	RealtimeModelID        string
	RealtimeResponsePolicy string
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
		permission := sourceCapturePermission{ID: source.ID, Kind: source.Kind, Enabled: source.Enabled, AutoStart: source.AutoStart, ContextID: source.ContextID}
		if source.Audio != nil {
			permission.AudioMode = source.Audio.Mode
			permission.DeviceID = source.Audio.DeviceID
			permission.DeviceRole = source.Audio.DeviceRole
		}
		if source.Realtime != nil {
			permission.RealtimeProviderID = source.Realtime.ProviderID
			permission.RealtimeModelID = source.Realtime.ModelID
			permission.RealtimeResponsePolicy = source.Realtime.ResponsePolicy
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

func commandPermissionAllowed(policy capturePermissionPolicy, command domain.CommandConfig) bool {
	for _, allowed := range policy.Commands {
		if allowed.ID == command.ID && allowed.Action == command.Action && allowed.EntryNodeID == command.EntryNodeID && allowed.CaptureScreen == command.CaptureScreen {
			return true
		}
	}
	return false
}

func sourcePermissionAllowed(policy capturePermissionPolicy, source domain.EventSource) bool {
	for _, allowed := range policy.Sources {
		if allowed.ID != source.ID || allowed.Kind != source.Kind || !allowed.Enabled || allowed.ContextID != source.ContextID {
			continue
		}
		providerID, modelID, responsePolicy := "", "", ""
		if source.Realtime != nil {
			providerID = source.Realtime.ProviderID
			modelID = source.Realtime.ModelID
			responsePolicy = source.Realtime.ResponsePolicy
		}
		if allowed.RealtimeProviderID == providerID && allowed.RealtimeModelID == modelID && allowed.RealtimeResponsePolicy == responsePolicy {
			return true
		}
	}
	return false
}

// FencePermissions invalidates capture/action closures before configuration save
// waits for existing turns. It also cancels sources that the proposed policy
// explicitly disables, so no new media is captured under the old permission.
func (m *Manager) FencePermissions(next domain.Config) {
	nextPolicy := capturePermissions(next)
	m.mu.Lock()
	if !m.permissionPolicySet {
		m.permissionPolicy = capturePermissions(m.cfg)
		m.permissionPolicySet = true
	}
	permissionChanged := !reflect.DeepEqual(m.permissionPolicy, nextPolicy)
	if permissionChanged {
		m.permissionGeneration++
		m.permissionPolicy = nextPolicy
	}
	var revoked []*sourceRuntime
	for _, runtime := range m.sources {
		permissionRevoked := !sourcePermissionAllowed(nextPolicy, runtime.cfg)
		routeChanged := runtime.realtimeFingerprint != realtimeSourceFingerprint(next, runtime.cfg)
		if permissionRevoked || routeChanged {
			revoked = append(revoked, runtime)
		}
	}
	m.mu.Unlock()
	for _, runtime := range revoked {
		revokeSource(runtime)
	}
}
