package engine

import (
	"sort"
	"strings"

	"aioverlay/internal/domain"
)

// PoolInput is a conservative graph-derived description used by Studio. It is
// not a captured payload and never starts a source or consumes model ordering.
type PoolInput struct {
	Known         bool     `json:"known"`
	Reachable     bool     `json:"reachable"`
	ImageRequired bool     `json:"image_required"`
	AudioRequired bool     `json:"audio_required"`
	TextAvailable bool     `json:"text_available"`
	Mixed         bool     `json:"mixed"`
	Source        string   `json:"source"`
	Sources       []string `json:"sources,omitempty"`
}

type inputState struct {
	id                            string
	image, audio, text, hasOutput bool
	imageResolved                 bool
	source                        string
}

func DescribePoolInput(pipeline domain.Pipeline, nodeID string) PoolInput {
	return describePoolInput(pipeline, nodeID, pipelineInputStarts(pipeline))
}

func pipelineInputStarts(pipeline domain.Pipeline) []inputState {
	starts := make([]inputState, 0, 1+len(pipeline.Commands)+len(pipeline.Sources))
	if pipeline.EntryNodeID != "" {
		starts = append(starts, inputState{id: pipeline.EntryNodeID, image: true, source: "legacy/manual screen"})
	}
	for _, command := range pipeline.Commands {
		starts = append(starts, commandInputState(pipeline, command))
	}
	for _, source := range pipeline.Sources {
		if !source.Enabled {
			continue
		}
		starts = append(starts, sourceInputState(pipeline, source))
	}
	return starts
}

func commandInputState(pipeline domain.Pipeline, command domain.CommandConfig) inputState {
	return inputState{
		id:     command.EntryNodeID,
		image:  command.CaptureScreen || contextMayProvideScreen(pipeline, command.ContextID),
		text:   command.ContextID != "",
		source: "command:" + command.Action,
	}
}

func sourceInputState(pipeline domain.Pipeline, source domain.EventSource) inputState {
	return inputState{
		id:     source.EntryNodeID,
		image:  source.Kind == domain.SourceScreenTimer || contextMayProvideScreen(pipeline, source.ContextID),
		audio:  source.Kind == domain.SourceAudio,
		text:   source.ContextID != "",
		source: "source:" + source.ID,
	}
}

func contextMayProvideScreen(pipeline domain.Pipeline, contextID string) bool {
	if contextID == "" {
		return false
	}
	for _, context := range pipeline.Contexts {
		if context.ID == contextID && context.Enabled {
			return context.KeepLatestScreen
		}
	}
	return false
}

func describePoolInput(pipeline domain.Pipeline, nodeID string, starts []inputState) PoolInput {
	rootID, pool := resolvePoolNode(pipeline, nodeID)
	if pool == nil {
		return PoolInput{}
	}
	if len(starts) == 0 {
		return PoolInput{Known: true}
	}

	image, audio, text := false, false, false
	sources := map[string]bool{}
	seen := map[inputState]bool{}
	queue := append([]inputState(nil), starts...)

	for len(queue) > 0 {
		state := queue[0]
		queue = queue[1:]
		if seen[state] {
			continue
		}
		seen[state] = true

		node, found := findNode(pipeline, state.id)
		if !found {
			continue
		}
		if state.id == rootID {
			stageImage := state.image && !state.imageResolved
			stageText := state.text || state.hasOutput
			if node.ContextID != "" {
				stageText = true
				stageImage = stageImage || contextMayProvideScreen(pipeline, node.ContextID)
			}
			image = image || stageImage
			audio = audio || state.audio
			text = text || stageText
			sources[state.source] = true
			continue
		}

		success := applySuccessfulNodeInput(node, state)
		queue = append(queue, nextInputStates(pipeline, state, success)...)
	}

	result := makePoolInput(image, audio, text, sources)
	if pool.Task == "transcribe" {
		result.AudioRequired = true
		result.ImageRequired = false
	}
	if result.Reachable && pool.VisionPreprocessor != nil && result.ImageRequired {
		result.ImageRequired = false
		result.TextAvailable = true
		result.Mixed = result.AudioRequired
		result.Source = nodeID + "/vision"
	}
	return result
}

func resolvePoolNode(pipeline domain.Pipeline, nodeID string) (string, *domain.ModelPoolConfig) {
	rootID := nodeID
	depth := 0
	node, ok := findNode(pipeline, rootID)
	for !ok && strings.HasSuffix(rootID, "/vision") {
		rootID = strings.TrimSuffix(rootID, "/vision")
		depth++
		node, ok = findNode(pipeline, rootID)
	}
	if !ok || node.ModelPool == nil {
		return "", nil
	}
	pool := node.ModelPool
	for range depth {
		pool = pool.VisionPreprocessor
		if pool == nil {
			return "", nil
		}
	}
	return rootID, pool
}

func applySuccessfulNodeInput(node domain.Node, state inputState) inputState {
	success := state
	switch node.Type {
	case domain.NodeCapture:
		success.image = true
		success.imageResolved = false
	case domain.NodeContext:
		success.text = true
	case domain.NodeModelPool:
		success.text = true
		success.hasOutput = true
		transcription := node.ModelPool != nil && node.ModelPool.Task == "transcribe"
		if success.image && !transcription {
			success.imageResolved = true
		}
		if transcription {
			success.audio = false
		}
	case domain.NodeDecision:
		success.text = true
		success.hasOutput = true
	}
	return success
}

func nextInputStates(pipeline domain.Pipeline, original, success inputState) []inputState {
	var next []inputState
	for _, edge := range pipeline.Edges {
		if edge.From != original.id || edge.MaxVisits <= 0 {
			continue
		}
		for _, outcome := range edge.On {
			state := success
			if outcome == domain.OutcomeAnyError || domain.IsErrorOutcome(outcome) {
				state = original
			}
			state.id = edge.To
			next = append(next, state)
		}
	}
	return next
}

func makePoolInput(image, audio, text bool, sources map[string]bool) PoolInput {
	result := PoolInput{
		Known: true, Reachable: image || audio || text,
		ImageRequired: image, AudioRequired: audio, TextAvailable: text,
		Mixed: boolInt(image)+boolInt(audio)+boolInt(text) > 1,
	}
	for source := range sources {
		result.Sources = append(result.Sources, source)
	}
	sort.Strings(result.Sources)
	result.Source = strings.Join(result.Sources, ", ")
	return result
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

// ContextIDsForRoute returns only explicit named context dependencies for the
// reachable AI stages. Trigger context is already carried separately in Input.
func ContextIDsForRoute(pipeline domain.Pipeline, entryID string, intent domain.CommandIntent) []string {
	nodes := behaviorNodeMap(pipeline)
	reachable := reachableBehaviorNodes(pipeline, entryID, intent, false)
	seen := map[string]bool{}
	var ids []string
	for _, item := range reachable {
		node := nodes[item.ID]
		if node.Type != domain.NodeModelPool && node.Type != domain.NodeDecision {
			continue
		}
		if node.Type == domain.NodeDecision && intent == domain.IntentRespond {
			continue
		}
		mode := node.ContextMode
		if mode == "" && node.ContextID != "" {
			mode = "named"
		}
		if mode != "named" || node.ContextID == "" || seen[node.ContextID] {
			continue
		}
		seen[node.ContextID] = true
		ids = append(ids, node.ContextID)
	}
	sort.Strings(ids)
	return ids
}
