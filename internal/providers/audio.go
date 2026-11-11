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

// Media reports what one connection can carry. The facts come from domain.ProviderPolicy - the
// table that is readable without constructing an adapter, because the pure candidate planner and
// the config validator need them and may not touch the provider registry. AudioFromCfg is the one
// per-connection refinement: the OpenAI-compatible family carries audio optionally, and only the
// operator knows whether this endpoint does.
func Media(p domain.ProviderConfig) MediaCapabilities {
	policy := domain.Policy(p.Kind)
	return MediaCapabilities{
		Audio:             policy.Media.Audio || (policy.AudioFromCfg && p.AudioInput),
		Image:             policy.Media.Image,
		TranscriptionOnly: policy.Media.TranscriptionOnly,
	}
}

// StreamsTokens reports whether a connection delivers its answer as tokens rather than as one
// body. It decides whether a first-token deadline means anything, so the adapter that will actually
// speak the protocol answers when it is available, and the kind table answers when it is not - the
// same split as Media, and the reason a simulation without adapters still predicts the real shape.
func StreamsTokens(cfg domain.ProviderConfig, adapter Provider) bool {
	if reporter, ok := adapter.(interface{ Incremental() bool }); ok {
		return reporter.Incremental()
	}
	return !domain.Policy(cfg.Kind).SingleBody
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
