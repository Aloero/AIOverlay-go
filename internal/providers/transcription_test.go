package providers

import (
	"aioverlay/internal/domain"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTranscriptionBuildsWAVInMemoryAndReturnsText(t *testing.T) {
	var sawRIFF bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/audio/transcriptions" {
			t.Errorf("path=%s", r.URL.Path)
		}
		mr := multipart.NewReader(r.Body, strings.TrimPrefix(r.Header.Get("Content-Type"), "multipart/form-data; boundary="))
		for {
			p, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Error(err)
				return
			}
			b, _ := io.ReadAll(p)
			if p.FormName() == "file" && strings.HasPrefix(string(b), "RIFF") {
				sawRIFF = true
			}
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"text":"heard text"}`)
	}))
	defer srv.Close()
	p := NewTranscription(domain.ProviderConfig{ID: "stt", Kind: domain.ProviderTranscription, BaseURL: srv.URL}, nil)
	audio := &domain.AudioClip{PCM16: make([]int16, 1600), SampleRate: 16000, Channels: 1, DurationMS: 100}
	ev, errs := p.Stream(context.Background(), Request{ProviderID: "stt", ModelID: "whisper-test", Task: "transcribe", Audio: audio, ResponseTimeout: time.Second}, "key")
	text := ""
	for e := range ev {
		text += e.Text
	}
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if text != "heard text" || !sawRIFF {
		t.Fatalf("text=%q RIFF=%v", text, sawRIFF)
	}
}
