package engine

import (
	"aioverlay/internal/domain"
	"strings"
)

// PoolInput describes possible screenshot paths without executing a graph.
// A retained original image is not the same thing as a required model payload:
// after a successful model stage, text-only models can use previous.output.
type PoolInput struct {
	Known         bool   `json:"known"`
	Reachable     bool   `json:"reachable"`
	ImageRequired bool   `json:"image_required"`
	TextAvailable bool   `json:"text_available"`
	Mixed         bool   `json:"mixed"`
	Source        string `json:"source"`
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
	type step struct {
		id   string
		text bool
	}
	queue := []step{{p.EntryNodeID, false}}
	seen := map[step]bool{}
	image, text := false, false
	for len(queue) > 0 {
		st := queue[0]
		queue = queue[1:]
		if seen[st] {
			continue
		}
		seen[st] = true
		node, ok := findNode(p, st.id)
		if !ok {
			continue
		}
		if st.id == rootID {
			if st.text {
				text = true
			} else {
				image = true
			}
		}
		if node.Type == domain.NodeOutput {
			continue
		}
		for _, ed := range p.Edges {
			if ed.From != st.id || ed.MaxVisits <= 0 {
				continue
			}
			for _, o := range ed.On {
				nextText := st.text || node.Type == domain.NodeModelPool && o == domain.OutcomeSuccess
				queue = append(queue, step{ed.To, nextText})
			}
		}
	}
	out := PoolInput{Known: true, Reachable: image || text, ImageRequired: image, TextAvailable: text, Mixed: image && text, Source: "screen"}
	if text {
		out.Source = "previous.output"
	}
	if out.Reachable && pool.VisionPreprocessor != nil {
		out.ImageRequired = false
		out.TextAvailable = true
		out.Mixed = false
		out.Source = nodeID + "/vision"
	}
	return out
}
