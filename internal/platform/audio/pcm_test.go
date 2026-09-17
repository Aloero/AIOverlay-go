package audio

import (
	"aioverlay/internal/domain"
	"encoding/binary"
	"math"
	"testing"
	"time"
)

func TestMonoPCMSilentDoesNotReadPayload(t *testing.T) {
	f := Format{Rate: 48000, Channels: 2, Bits: 32, Align: 8, Float: true}
	got, err := MonoPCM(nil, 16, f, true)
	if err != nil || len(got) != 16 {
		t.Fatalf("len=%d err=%v", len(got), err)
	}
	for _, v := range got {
		if v != 0 {
			t.Fatal("silent packet not zero")
		}
	}
}
func TestMonoPCMAveragesStereo16(t *testing.T) {
	raw := make([]byte, 4)
	binary.LittleEndian.PutUint16(raw[0:], uint16(int16(12000)))
	neg := int16(-4000)
	binary.LittleEndian.PutUint16(raw[2:], uint16(neg))
	got, err := MonoPCM(raw, 1, Format{Rate: 48000, Channels: 2, Bits: 16, Align: 4}, false)
	if err != nil {
		t.Fatal(err)
	}
	if got[0] < 3900 || got[0] > 4100 {
		t.Fatalf("got %d", got[0])
	}
}
func TestMonoPCMNaNIsSanitized(t *testing.T) {
	raw := make([]byte, 4)
	binary.LittleEndian.PutUint32(raw, math.Float32bits(float32(math.NaN())))
	got, err := MonoPCM(raw, 1, Format{Rate: 48000, Channels: 1, Bits: 32, Align: 4, Float: true}, false)
	if err != nil || got[0] != 0 {
		t.Fatalf("%v %v", got, err)
	}
}
func TestSegmenterKeepsShortImportantSpeechAndBoundsMemory(t *testing.T) {
	s := Segmenter{Config: domain.AudioSourceConfig{Mode: "system", SegmentMS: 1000, SilenceMS: 200, MinimumMS: 80, Threshold: .01}}
	now := time.Now()
	voice := make([]int16, 1600)
	for i := range voice {
		voice[i] = 12000
	}
	var out []Segment
	for i := 0; i < 2; i++ {
		out = append(out, s.Push(Block{Samples: voice, Rate: 16000, At: now.Add(time.Duration(i) * 100 * time.Millisecond)})...)
	}
	out = append(out, s.Push(Block{Rate: 16000, At: now.Add(500 * time.Millisecond)})...)
	if len(out) != 1 {
		t.Fatalf("segments=%d", len(out))
	}
	if out[0].Clip.DurationMS < 80 {
		t.Fatalf("duration=%d", out[0].Clip.DurationMS)
	}
	if len(out[0].Clip.PCM16) > 16000*12/10 {
		t.Fatal("segment exceeded configured bound plus preroll")
	}
}

func TestMonoPCM24BitSigned(t *testing.T) {
	raw := []byte{0x00, 0x00, 0x40}
	got, err := MonoPCM(raw, 1, Format{Rate: 48000, Channels: 1, Bits: 24, Align: 3}, false)
	if err != nil {
		t.Fatal(err)
	}
	if got[0] < 16000 || got[0] > 16400 {
		t.Fatalf("got %d", got[0])
	}
}
func TestSegmenterDiscontinuityDoesNotJoinSpeechAcrossGap(t *testing.T) {
	s := Segmenter{Config: domain.AudioSourceConfig{Mode: "system", SegmentMS: 2000, SilenceMS: 200, MinimumMS: 80, Threshold: .01}}
	voice := make([]int16, 1600)
	for i := range voice {
		voice[i] = 12000
	}
	now := time.Now()
	s.Push(Block{Samples: voice, Rate: 16000, At: now})
	s.Push(Block{Samples: voice, Rate: 16000, At: now.Add(100 * time.Millisecond), Discontinuity: true})
	out := s.Push(Block{Rate: 16000, At: now.Add(500 * time.Millisecond)})
	if len(out) != 1 {
		t.Fatalf("segments=%d", len(out))
	}
	if out[0].Clip.DurationMS > 250 {
		t.Fatalf("old speech crossed discontinuity: %dms", out[0].Clip.DurationMS)
	}
}

func TestDecodeWASAPIBufferFlagsDistinguishesSilentFromGaps(t *testing.T) {
	silent := decodeWASAPIBufferFlags(wasapiBufferFlagSilent)
	if !silent.Silent || silent.Discontinuity || silent.TimestampError {
		t.Fatalf("silent flags decoded as %+v", silent)
	}

	gap := decodeWASAPIBufferFlags(wasapiBufferFlagDataDiscontinuity | wasapiBufferFlagTimestampError)
	if gap.Silent || !gap.Discontinuity || !gap.TimestampError {
		t.Fatalf("gap flags decoded as %+v", gap)
	}
}

func TestSegmenterTimestampErrorDoesNotJoinSpeechAcrossGap(t *testing.T) {
	s := Segmenter{Config: domain.AudioSourceConfig{Mode: "system", SegmentMS: 2000, SilenceMS: 200, MinimumMS: 80, Threshold: .01}}
	voice := make([]int16, 1600)
	for i := range voice {
		voice[i] = 12000
	}
	now := time.Now()
	s.Push(Block{Samples: voice, Rate: 16000, At: now})
	s.Push(Block{Samples: voice, Rate: 16000, At: now.Add(100 * time.Millisecond), TimestampError: true})
	out := s.Push(Block{Rate: 16000, At: now.Add(500 * time.Millisecond)})
	if len(out) != 1 {
		t.Fatalf("segments=%d", len(out))
	}
	if out[0].Clip.DurationMS > 250 {
		t.Fatalf("old speech crossed timestamp error: %dms", out[0].Clip.DurationMS)
	}
}
