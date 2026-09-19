package audio

import (
	"aioverlay/internal/domain"
	"testing"
	"time"
)

func TestR2AuditOverlapDoesNotRelabelOldAudioAfterSilence(t *testing.T) {
	s := Segmenter{Config: domain.AudioSourceConfig{SegmentMS: 1000, MinimumMS: 80, SilenceMS: 200, Threshold: .01}}
	at := time.Unix(1700000000, 0)
	voice := make([]int16, 16000)
	for i := range voice {
		voice[i] = 12000
	}
	first := s.Push(Block{Rate: 16000, Samples: voice, At: at})
	if len(first) != 1 {
		t.Fatal(len(first))
	}
	s.Push(Block{Rate: 16000, At: at.Add(2 * time.Second)})
	s.Push(Block{Rate: 16000, Samples: voice[:1600], At: at.Add(3 * time.Second)})
	last := s.Push(Block{Rate: 16000, At: at.Add(4 * time.Second)})
	if len(last) != 1 {
		t.Fatal(len(last))
	}
	if last[0].Clip.Continuation || last[0].Started.Before(at.Add(3*time.Second)) {
		t.Fatalf("old audio replayed with false range after silence: start=%v overlap=%d", last[0].Started, last[0].Clip.OverlapMS)
	}
}
