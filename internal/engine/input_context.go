package engine

import (
	"aioverlay/internal/domain"
	"sort"
	"strings"
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

func DescribePoolInput(p domain.Pipeline, nodeID string) PoolInput {
	rootID := nodeID
	depth := 0
	n, ok := findNode(p, rootID)
	for !ok && strings.HasSuffix(rootID, "/vision") {
		rootID = strings.TrimSuffix(rootID, "/vision")
		depth++
		n, ok = findNode(p, rootID)
	}
	if !ok || n.ModelPool == nil {
		return PoolInput{}
	}
	pool := n.ModelPool
	for i := 0; i < depth; i++ {
		pool = pool.VisionPreprocessor
		if pool == nil {
			return PoolInput{}
		}
	}
	starts := []inputState{}
	if p.EntryNodeID != "" {
		starts = append(starts, inputState{id: p.EntryNodeID, image: true, source: "legacy/manual screen"})
	}
	for _, c := range p.Commands {
		starts = append(starts, inputState{id: c.EntryNodeID, image: c.CaptureScreen, text: c.ContextID != "", source: "command:" + c.Action})
	}
	for _, src := range p.Sources {
		if !src.Enabled {
			continue
		}
		st := inputState{id: src.EntryNodeID, text: src.ContextID != "", source: "source:" + src.ID}
		if src.Kind == domain.SourceAudio {
			st.audio = true
		}
		if src.Kind == domain.SourceScreenTimer {
			st.image = true
		}
		starts = append(starts, st)
	}
	if len(starts) == 0 {
		return PoolInput{Known: true}
	}
	seen := map[inputState]bool{}
	queue := starts
	image, audio, text := false, false, false
	sources := map[string]bool{}
	for len(queue) > 0 {
		st := queue[0]
		queue = queue[1:]
		if seen[st] {
			continue
		}
		seen[st] = true
		node, found := findNode(p, st.id)
		if !found {
			continue
		}
		if st.id == rootID {
			image = image || (st.image && !st.hasOutput)
			audio = audio || st.audio
			text = text || st.text || st.hasOutput
			sources[st.source] = true
			continue
		}
		success := st
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
		for _, ed := range p.Edges {
			if ed.From != st.id || ed.MaxVisits <= 0 {
				continue
			}
			for _, o := range ed.On {
				q := success
				if o == domain.OutcomeAnyError || domain.IsErrorOutcome(o) {
					q = st
				}
				q.id = ed.To
				queue = append(queue, q)
			}
		}
	}
	out := PoolInput{Known: true, Reachable: image || audio || text, ImageRequired: image, AudioRequired: audio, TextAvailable: text, Mixed: (boolInt(image) + boolInt(audio) + boolInt(text)) > 1}
	for src := range sources {
		out.Sources = append(out.Sources, src)
	}
	sort.Strings(out.Sources)
	out.Source = strings.Join(out.Sources, ", ")
	if pool.Task == "transcribe" {
		out.AudioRequired = true
		out.ImageRequired = false
	}
	if out.Reachable && pool.VisionPreprocessor != nil && out.ImageRequired {
		out.ImageRequired = false
		out.TextAvailable = true
		out.Mixed = out.AudioRequired
		out.Source = nodeID + "/vision"
	}
	return out
}
func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
