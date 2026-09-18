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
	segmenter := Segmenter{Config: domain.AudioSourceConfig{Mode: "system", SegmentMS: 2000, SilenceMS: 200, MinimumMS: 80, Threshold: .01}}
	voice := make([]int16, 1600)
	for i := range voice {
		voice[i] = 12000
	}
	start := time.Now()
	segmenter.Push(Block{Samples: voice, Rate: 16000, At: start})
	interrupted := segmenter.Push(Block{Samples: voice, Rate: 16000, At: start.Add(100 * time.Millisecond), TimestampError: true})
	if len(interrupted) != 1 || interrupted[0].Clip.Final || interrupted[0].Clip.DurationMS != 100 {
		t.Fatalf("timestamp error must preserve only the incomplete prior span: %+v", interrupted)
	}
	if out := segmenter.Push(Block{Rate: 16000, At: start.Add(500 * time.Millisecond)}); len(out) != 0 {
		t.Fatal("timestamp-error packet was retained as normal speech")
	}
	segmenter.Push(Block{Samples: voice, Rate: 16000, At: start.Add(time.Second)})
	completed := segmenter.Push(Block{Rate: 16000, At: start.Add(1500 * time.Millisecond)})
	if len(completed) != 1 || !completed[0].Clip.Final || !completed[0].Started.Equal(start.Add(time.Second)) {
		t.Fatalf("valid speech after timestamp error did not resume independently: %+v", completed)
	}
}

func TestSegmenterInterruptFlushesIncompleteSpeech(t *testing.T) {
	s := Segmenter{Config: domain.AudioSourceConfig{Mode: "system", SegmentMS: 2000, SilenceMS: 200, MinimumMS: 80, Threshold: .01}}
	voice := make([]int16, 1600)
	for i := range voice {
		voice[i] = 12000
	}
	now := time.Now()
	s.Push(Block{Samples: voice, Rate: 16000, At: now})
	seg, ok := s.Interrupt()
	if !ok {
		t.Fatal("in-progress speech was discarded on interruption")
	}
	if seg.Clip.Final {
		t.Fatal("transport interruption was marked as a normal final segment")
	}
	if seg.Started != now || seg.Ended.Sub(seg.Started) != 100*time.Millisecond {
		t.Fatalf("unexpected interrupted range %v..%v", seg.Started, seg.Ended)
	}
}

func TestSegmenterInterruptWithoutSamplesIsSafe(t *testing.T) {
	s := Segmenter{Config: domain.AudioSourceConfig{Mode: "system", SegmentMS: 1000, SilenceMS: 200, MinimumMS: 80, Threshold: .01}}
	if _, ok := s.Interrupt(); ok {
		t.Fatal("empty segment unexpectedly emitted")
	}
}

func TestSegmenterDiscontinuityKeepsNewPacketAsSeparateSpeech(t *testing.T) {
	segmenter := Segmenter{Config: domain.AudioSourceConfig{
		Mode: "system", SegmentMS: 2000, SilenceMS: 200, MinimumMS: 80, Threshold: .01,
	}}
	firstVoice := make([]int16, 1600)
	secondVoice := make([]int16, 1600)
	for i := range firstVoice {
		firstVoice[i] = 9000
		secondVoice[i] = 14000
	}
	start := time.Now()
	if out := segmenter.Push(Block{Samples: firstVoice, Rate: 16000, At: start}); len(out) != 0 {
		t.Fatalf("first packet unexpectedly completed %d segments", len(out))
	}

	interrupted := segmenter.Push(Block{
		Samples: secondVoice, Rate: 16000, At: start.Add(100 * time.Millisecond), Discontinuity: true,
	})
	if len(interrupted) != 1 {
		t.Fatalf("discontinuity did not flush exactly one old segment: %d", len(interrupted))
	}
	if interrupted[0].Clip.Final {
		t.Fatal("old speech flushed by discontinuity was marked as a normal final segment")
	}
	if interrupted[0].Started != start || interrupted[0].Ended.Sub(interrupted[0].Started) != 100*time.Millisecond {
		t.Fatalf("old speech boundaries=%v..%v", interrupted[0].Started, interrupted[0].Ended)
	}

	completed := segmenter.Push(Block{Rate: 16000, At: start.Add(500 * time.Millisecond)})
	if len(completed) != 1 {
		t.Fatalf("new packet after discontinuity was lost: completed=%d", len(completed))
	}
	if !completed[0].Clip.Final {
		t.Fatal("new speech after discontinuity did not complete normally")
	}
	if completed[0].Started.Before(start.Add(100 * time.Millisecond)) {
		t.Fatalf("new speech was joined to pre-gap audio: started=%v", completed[0].Started)
	}
	if len(completed[0].Clip.PCM16) != len(secondVoice) {
		t.Fatalf("new speech samples=%d want=%d", len(completed[0].Clip.PCM16), len(secondVoice))
	}
}

func TestTimestampErrorPacketCannotBecomeOrdinarySpeech(t *testing.T) {
	segmenter := Segmenter{Config: domain.AudioSourceConfig{Mode: "system", SegmentMS: 1000, SilenceMS: 200, MinimumMS: 80, Threshold: .01}}
	voice := make([]int16, 16000)
	for i := range voice {
		voice[i] = 12000
	}
	start := time.Now()
	segments := segmenter.Push(Block{Samples: voice, Rate: 16000, At: start, TimestampError: true})
	segments = append(segments, segmenter.Push(Block{Rate: 16000, At: start.Add(2 * time.Second)})...)
	if len(segments) != 0 {
		t.Fatal("timestamp-error samples became speech eligible for normal processing")
	}
}
