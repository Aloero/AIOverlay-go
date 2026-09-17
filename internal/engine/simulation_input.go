package engine

import (
	"fmt"
	"strings"

	"aioverlay/internal/domain"
)

func simulationInput(pipeline *domain.Pipeline, request SimulationRequest) (Input, nodeValue, string, error) {
	if request.TriggerID == "" {
		return legacySimulationInput(pipeline, request), legacySimulationValue(request), pipeline.EntryNodeID, nil
	}
	if command, ok := simulationCommand(pipeline, request.TriggerID); ok {
		input := commandSimulationInput(command, request)
		return input, nodeValue{image: input.ImageJPEG, text: request.InputText}, command.EntryNodeID, nil
	}
	if source, ok := simulationSource(pipeline, request.TriggerID); ok {
		return sourceSimulationInput(source, request)
	}
	return Input{}, nodeValue{}, "", fmt.Errorf("simulation trigger %q is not configured", request.TriggerID)
}

func legacySimulationInput(pipeline *domain.Pipeline, request SimulationRequest) Input {
	return Input{Text: request.InputText, ContextText: request.ContextText, ImageJPEG: []byte{1}, EntryNodeID: pipeline.EntryNodeID}
}

func legacySimulationValue(request SimulationRequest) nodeValue {
	return nodeValue{image: []byte{1}, text: request.InputText}
}

func simulationCommand(pipeline *domain.Pipeline, id string) (domain.CommandConfig, bool) {
	for _, command := range pipeline.Commands {
		if command.ID == id {
			return command, true
		}
	}
	return domain.CommandConfig{}, false
}

func simulationSource(pipeline *domain.Pipeline, id string) (domain.EventSource, bool) {
	for _, source := range pipeline.Sources {
		if source.ID == id && source.Enabled {
			return source, true
		}
	}
	return domain.EventSource{}, false
}

func commandSimulationInput(command domain.CommandConfig, request SimulationRequest) Input {
	input := Input{
		Text:        request.InputText,
		ContextText: request.ContextText,
		EntryNodeID: command.EntryNodeID,
		Intent:      string(command.Intent),
		SourceID:    command.ID,
	}
	if command.CaptureScreen {
		input.ImageJPEG = []byte{1}
	}
	return input
}

func sourceSimulationInput(source domain.EventSource, request SimulationRequest) (Input, nodeValue, string, error) {
	input := Input{
		Text:        request.InputText,
		ContextText: request.ContextText,
		EntryNodeID: source.EntryNodeID,
		Intent:      string(source.Intent),
		SourceID:    source.ID,
	}
	value := nodeValue{text: request.InputText}

	switch source.Kind {
	case domain.SourceAudio:
		clip := syntheticAudioClip()
		input.Audio = clip
		value.audio = clip
	case domain.SourceScreenTimer:
		input.ImageJPEG = []byte{1}
		value.image = input.ImageJPEG
	default:
		return Input{}, nodeValue{}, "", fmt.Errorf("unsupported simulation source %s", source.Kind)
	}
	return input, value, source.EntryNodeID, nil
}

func syntheticAudioClip() *domain.AudioClip {
	return &domain.AudioClip{PCM16: []int16{1}, SampleRate: 16000, Channels: 1, DurationMS: 1, Final: true}
}

func joinSimulationContext(contextText, text string) string {
	return strings.TrimSpace(contextText + "\n" + text)
}
