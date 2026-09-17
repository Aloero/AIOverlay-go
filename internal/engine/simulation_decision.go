package engine

import (
	"aioverlay/internal/domain"
)

func (s *simulator) decision(node domain.Node, input nodeValue) (nodeValue, domain.Outcome, error) {
	if s.r.input.Intent == string(domain.IntentRespond) {
		s.emit(domain.TraceEvent{Kind: "decision", NodeID: node.ID, Outcome: domain.OutcomeRespond, Message: "Manual respond intent bypasses only the decision gate"})
		return input, domain.OutcomeRespond, nil
	}
	if s.r.input.Intent == string(domain.IntentContext) {
		return input, domain.OutcomeContextOnly, nil
	}

	raw, outcome, err := s.pool(node, input)
	if err != nil {
		return input, outcome, err
	}
	decision, err := parseDecision(raw.text)
	if err != nil {
		return input, domain.OutcomeInvalidResponse, err
	}

	result := input
	if decision.Transcript != "" {
		result.transcript = decision.Transcript
	}
	semanticOutcome := domain.Outcome(decision.Action)
	if semanticOutcome == domain.OutcomeRespond && decision.Text != "" {
		result.text = decision.Text
		result.hasOutput = true
	}
	s.emit(domain.TraceEvent{Kind: "decision", NodeID: node.ID, Outcome: semanticOutcome, Message: "Conditional structured decision"})
	return result, semanticOutcome, nil
}
