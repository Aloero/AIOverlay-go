package control

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"aioverlay/internal/domain"
	"aioverlay/internal/session"
)

type previewLimitBackend struct {
	*auditBackend
	sessionBackend
	duration time.Duration
	deadline time.Time
}

func (backend *previewLimitBackend) PreviewAudio(ctx context.Context, _ domain.AudioSourceConfig, duration time.Duration) (session.AudioPreview, error) {
	backend.duration = duration
	backend.deadline, _ = ctx.Deadline()
	return session.AudioPreview{PCM16: []int16{0}, SampleRate: 16000}, nil
}

func TestAudioPreviewHandlerBoundsDurationBeforeComputingDeadline(t *testing.T) {
	for _, tc := range []struct {
		supplied int64
		want     time.Duration
	}{
		{0, 2 * time.Second}, {-9223372036854775808, 250 * time.Millisecond}, {9223372036854775807, 5 * time.Second},
	} {
		t.Run(fmt.Sprint(tc.supplied), func(t *testing.T) {
			backend := &previewLimitBackend{auditBackend: &auditBackend{}}
			server := New("127.0.0.1:0", backend)
			body := fmt.Sprintf(`{"config":{"mode":"system","segment_ms":1000,"silence_ms":200,"minimum_ms":80,"threshold":0.01},"duration_ms":%d}`, tc.supplied)
			request := httptest.NewRequest(http.MethodPost, "/api/audio/preview", strings.NewReader(body))
			response := httptest.NewRecorder()
			before := time.Now()
			server.DesktopHandler().ServeHTTP(response, request)
			if response.Code != 200 {
				t.Fatalf("HTTP %d: %s", response.Code, response.Body.String())
			}
			if backend.duration != tc.want {
				t.Fatalf("duration=%v, want=%v", backend.duration, tc.want)
			}
			if backend.deadline.Before(before) || time.Until(backend.deadline) > 7*time.Second {
				t.Fatal("overflow or unbounded deadline in preview handler")
			}
		})
	}
}
