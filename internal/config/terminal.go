package config

import "aioverlay/internal/domain"

func entryHasTerminal(pipeline domain.Pipeline, nodes map[string]domain.Node, entryID string, intent domain.CommandIntent) bool {
	queue := []string{entryID}
	seen := map[string]bool{}
	for len(queue) > 0 {
		nodeID := queue[0]
		queue = queue[1:]
		if seen[nodeID] {
			continue
		}
		seen[nodeID] = true

		node, ok := nodes[nodeID]
		if !ok {
			continue
		}
		if isNormalTerminal(node, intent) {
			return true
		}
		outcome, ok := normalNodeOutcome(node, intent)
		if !ok {
			continue
		}
		for _, edge := range pipeline.Edges {
			if edge.From == nodeID && edge.MaxVisits > 0 && edgeHandlesOutcome(edge, outcome) {
				queue = append(queue, edge.To)
			}
		}
	}
	return false
}

func isNormalTerminal(node domain.Node, intent domain.CommandIntent) bool {
	switch intent {
	case domain.IntentContext:
		return node.Type == domain.NodeContext
	case domain.IntentDecide:
		// no_action is a normal semantic result produced by Decision itself.
		return node.Type == domain.NodeDecision
	default:
		return node.Type == domain.NodeOutput
	}
}

func normalNodeOutcome(node domain.Node, intent domain.CommandIntent) (domain.Outcome, bool) {
	if node.Type == domain.NodeOutput {
		return "", false
	}
	if node.Type == domain.NodeDecision && intent == domain.IntentRespond {
		return domain.OutcomeRespond, true
	}
	return domain.OutcomeSuccess, true
}

func edgeHandlesOutcome(edge domain.Edge, outcome domain.Outcome) bool {
	for _, candidate := range edge.On {
		if candidate == outcome {
			return true
		}
	}
	return false
}
