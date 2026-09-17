package providers

import (
	"aioverlay/internal/domain"
	"bytes"
)

// Media support is an adapter capability, distinct from Models.dev metadata.
// Arbitrary compatible endpoints opt into the documented chat input_audio shape.
type MediaCapabilities struct {
	Audio             bool `json:"audio"`
	TranscriptionOnly bool `json:"transcription_only"`
	Realtime          bool `json:"realtime"`
}

func Media(p domain.ProviderConfig) MediaCapabilities {
	switch p.Kind {
	case domain.ProviderGemini:
		return MediaCapabilities{Audio: true}
	case domain.ProviderTranscription:
		return MediaCapabilities{Audio: true, TranscriptionOnly: true}
	case domain.ProviderOpenAICompatible:
		return MediaCapabilities{Audio: p.AudioInput}
	default:
		return MediaCapabilities{}
	}
}
func validateAudioRequest(p domain.ProviderConfig, r Request) error {
	c := Media(p)
	if r.Audio != nil {
		if !c.Audio {
			return &RequestError{Outcome: domain.OutcomeUnsupportedModality, Message: "This transport does not support audio input"}
		}
		if len(r.Audio.WAV) < 44 || len(r.Audio.WAV) > 12<<20 || !bytes.Equal(r.Audio.WAV[:4], []byte("RIFF")) || !bytes.Equal(r.Audio.WAV[8:12], []byte("WAVE")) {
			return &RequestError{Outcome: domain.OutcomeValidationFailed, Message: "Invalid or excessive in-memory WAV payload"}
		}
	}
	if c.TranscriptionOnly && (r.Audio == nil || len(r.ImageJPEG) > 0 || r.Task != "transcribe") {
		return &RequestError{Outcome: domain.OutcomeUnsupportedModality, Message: "Transcription transport requires an audio-only transcription stage"}
	}
	return nil
}
