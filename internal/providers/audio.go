package providers

import (
	"aioverlay/internal/domain"
	"encoding/binary"
	"fmt"
)

// Media support is an adapter capability, distinct from Models.dev metadata.
type MediaCapabilities struct {
	Audio             bool `json:"audio"`
	Image             bool `json:"image"`
	TranscriptionOnly bool `json:"transcription_only"`
}

func Media(p domain.ProviderConfig) MediaCapabilities {
	switch p.Kind {
	case domain.ProviderGemini:
		return MediaCapabilities{Audio: true, Image: true}
	case domain.ProviderTranscription:
		return MediaCapabilities{Audio: true, TranscriptionOnly: true}
	case domain.ProviderOpenAICompatible:
		return MediaCapabilities{Audio: p.AudioInput, Image: true}
	case domain.ProviderEmbeddedOpenCode:
		return MediaCapabilities{Image: true}
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
		if r.Audio.SampleRate < 8000 || r.Audio.SampleRate > 192000 || r.Audio.Channels != 1 || len(r.Audio.PCM16) == 0 || len(r.Audio.PCM16) > 6_000_000 {
			return &RequestError{Outcome: domain.OutcomeValidationFailed, Message: "Invalid or excessive in-memory PCM audio payload"}
		}
	}
	if c.TranscriptionOnly && (r.Audio == nil || len(r.ImageJPEG) > 0 || r.Task != "transcribe") {
		return &RequestError{Outcome: domain.OutcomeUnsupportedModality, Message: "Transcription transport requires an audio-only transcription stage"}
	}
	return nil
}

// wavPCM16 is a transport container builder. Audio is kept as PCM in the core and
// is only wrapped when a bounded HTTP adapter specifically needs WAV.
func wavPCM16(c *domain.AudioClip) ([]byte, error) {
	if c == nil || c.Channels != 1 || c.SampleRate < 8000 || c.SampleRate > 192000 || len(c.PCM16) == 0 {
		return nil, fmt.Errorf("invalid PCM audio")
	}
	data := len(c.PCM16) * 2
	if data > 12<<20 {
		return nil, fmt.Errorf("audio payload exceeds transport limit")
	}
	b := make([]byte, 44+data)
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], uint32(len(b)-8))
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1)
	binary.LittleEndian.PutUint16(b[22:], 1)
	binary.LittleEndian.PutUint32(b[24:], uint32(c.SampleRate))
	binary.LittleEndian.PutUint32(b[28:], uint32(c.SampleRate*2))
	binary.LittleEndian.PutUint16(b[32:], 2)
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], uint32(data))
	for i, s := range c.PCM16 {
		binary.LittleEndian.PutUint16(b[44+i*2:], uint16(s))
	}
	return b, nil
}
