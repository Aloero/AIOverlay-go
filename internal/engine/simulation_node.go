package engine

import (
	"fmt"
	"strings"

	"aioverlay/internal/domain"
)

type simulationStep struct {
	value    nodeValue
	outcome  domain.Outcome
	err      error
	terminal bool
	output   bool
}

func (s *simulator) executeNode(node domain.Node, input nodeValue) (simulationStep, error) {
	step := simulationStep{value: input, outcome: domain.OutcomeSuccess}

	switch node.Type {
	case domain.NodeScreen:
		if len(input.image) == 0 {
			step.outcome = domain.OutcomeInvalidResponse
			step.err = fmt.Errorf("screen image is empty")
		}
	case domain.NodeAudio:
		if input.audio == nil {
			step.outcome = domain.OutcomeUnsupportedModality
			step.err = fmt.Errorf("audio input is empty")
		}
	case domain.NodeCapture:
		step.value.image = []byte{1}
		step.value.imageResolved = false
		s.emit(domain.TraceEvent{Kind: "capture_start", NodeID: node.ID, Message: "Synthetic screen action; no capture performed"})
	case domain.NodeContext:
		step.value.text = joinSimulationContext(s.req.ContextText, input.text)
		if s.r.input.Intent == string(domain.IntentContext) {
			step.outcome = domain.OutcomeContextOnly
			step.terminal = true
			s.r.waited = true
		}
	case domain.NodeDecision:
		step.value, step.outcome, step.err = s.decision(node, input)
		step.terminal = step.err == nil && (step.outcome == domain.OutcomeNoAction || step.outcome == domain.OutcomeContextOnly)
		if step.terminal {
			s.r.waited = true
		}
	case domain.NodeModelPool:
		step.value, step.outcome, step.err = s.pool(node, input)
		if step.err == nil {
			applySimulationModelResult(node, input, &step.value)
		}
	case domain.NodeValidator:
		step.outcome, step.err = runValidator(node.Validator, input.text)
	case domain.NodeOutput:
		return s.executeOutputNode(input), nil
	default:
		return simulationStep{}, fmt.Errorf("unsupported simulation node type %s", node.Type)
	}
	return step, nil
}

func applySimulationModelResult(node domain.Node, input nodeValue, output *nodeValue) {
	if node.ModelPool == nil {
		return
	}
	if node.ModelPool.Task == "transcribe" || node.ModelPool.ResultName == "audio.text" {
		output.transcript = output.text
		output.audio = nil
		output.vision = input.vision
	}
	if node.ModelPool.ResultName == "vision.text" {
		output.vision = output.text
	}
}

func (s *simulator) executeOutputNode(input nodeValue) simulationStep {
	if s.r.input.Intent == string(domain.IntentContext) {
		s.r.waited = true
		s.r.lastOutcome = domain.OutcomeContextOnly
		return simulationStep{value: input, outcome: domain.OutcomeContextOnly, output: true}
	}
	if !input.hasOutput && strings.TrimSpace(input.text) == "" {
		s.report.Error = "output reached without an accepted answer"
		return simulationStep{value: input, outcome: domain.OutcomeInvalidResponse, output: true}
	}
	s.report.Text = input.text
	return simulationStep{value: input, outcome: domain.OutcomeSuccess, output: true}
}
