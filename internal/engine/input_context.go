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
		starts = append(starts, inputState{
			id: command.EntryNodeID, image: command.CaptureScreen,
			text: command.ContextID != "", source: "command:" + command.Action,
		})
	}
	for _, source := range pipeline.Sources {
		if !source.Enabled {
			continue
		}
		state := inputState{id: source.EntryNodeID, text: source.ContextID != "", source: "source:" + source.ID}
		state.audio = source.Kind == domain.SourceAudio
		state.image = source.Kind == domain.SourceScreenTimer
		starts = append(starts, state)
	}
	return starts
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
			image = image || (state.image && !state.hasOutput)
			audio = audio || state.audio
			text = text || state.text || state.hasOutput
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
	case domain.NodeContext:
		success.text = true
	case domain.NodeModelPool, domain.NodeDecision:
		success.text = true
		success.hasOutput = true
		if node.ModelPool != nil && node.ModelPool.Task == "transcribe" {
			success.audio = false
		}
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
