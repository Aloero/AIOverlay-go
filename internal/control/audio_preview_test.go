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
	paudio "aioverlay/internal/platform/audio"
	"aioverlay/internal/session"
)

type previewLimitBackend struct {
	*auditBackend
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

type localSTTPreviewBackend struct {
	*auditBackend
	providerID string
	duration   time.Duration
}

func (backend *localSTTPreviewBackend) PreviewLocalSTT(_ context.Context, _ domain.AudioSourceConfig, providerID string, duration time.Duration) (session.LocalSTTPreview, error) {
	backend.providerID = providerID
	backend.duration = duration
	return session.LocalSTTPreview{
		ProviderID: providerID, ModelID: "local-model", Text: "локальный текст",
		SampleRate: 16000, DurationMS: 2000, Peak: .5, RMS: .1,
	}, nil
}

func TestLocalSTTPreviewHandlerReturnsTextWithoutRawMedia(t *testing.T) {
	backend := &localSTTPreviewBackend{auditBackend: &auditBackend{}}
	server := New("127.0.0.1:0", backend)
	body := `{"config":{"mode":"system","segment_ms":1000,"silence_ms":200,"minimum_ms":80,"threshold":0.01},"provider_id":"local","duration_ms":2000}`
	request := httptest.NewRequest(http.MethodPost, "/api/audio/local-stt-preview", strings.NewReader(body))
	response := httptest.NewRecorder()
	server.DesktopHandler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("HTTP %d: %s", response.Code, response.Body.String())
	}
	if backend.providerID != "local" || backend.duration != 2*time.Second {
		t.Fatalf("provider=%q duration=%v", backend.providerID, backend.duration)
	}
	payload := response.Body.String()
	for _, forbidden := range []string{"pcm16", "wav_base64", "image_base64"} {
		if strings.Contains(strings.ToLower(payload), forbidden) {
			t.Fatalf("local STT preview leaked raw media field %q: %s", forbidden, payload)
		}
	}
	for _, required := range []string{"local-model", "локальный текст", "duration_ms", "rms"} {
		if !strings.Contains(payload, required) {
			t.Fatalf("preview response missing %q: %s", required, payload)
		}
	}
}

func TestLocalSTTPreviewHandlerRejectsMissingProviderAndInvalidAudio(t *testing.T) {
	backend := &localSTTPreviewBackend{auditBackend: &auditBackend{}}
	server := New("127.0.0.1:0", backend)
	for _, body := range []string{
		`{"config":{"mode":"system","segment_ms":1000,"silence_ms":200,"minimum_ms":80,"threshold":0.01}}`,
		`{"provider_id":"local","config":{"mode":"invalid","segment_ms":1000,"silence_ms":200,"minimum_ms":80,"threshold":0.01}}`,
	} {
		request := httptest.NewRequest(http.MethodPost, "/api/audio/local-stt-preview", strings.NewReader(body))
		response := httptest.NewRecorder()
		server.DesktopHandler().ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("HTTP %d for %s", response.Code, body)
		}
	}
}

type processAudioBackend struct {
	*auditBackend
}

func (*processAudioBackend) AudioCapabilities() paudio.Capabilities {
	return paudio.Capabilities{SystemLoopback: true, Microphone: true, ProcessLoopback: true, MinimumProcessLoopbackBuild: 20348}
}
func (*processAudioBackend) AudioProcesses() ([]paudio.Process, error) {
	return []paudio.Process{{PID: 42, Name: "Zoom.exe", Path: `C:\Program Files\Zoom\bin\Zoom.exe`, StartedAt: time.Unix(100, 0)}}, nil
}

func TestAudioProcessCapabilityAndIdentityHandlersExposeNoFallback(t *testing.T) {
	backend := &processAudioBackend{auditBackend: &auditBackend{}}
	server := New("127.0.0.1:0", backend)
	for _, test := range []struct {
		path     string
		required []string
	}{
		{path: "/api/audio/capabilities", required: []string{"process_loopback", "minimum_process_loopback_build", "20348"}},
		{path: "/api/audio/processes", required: []string{"Zoom.exe", `C:\\Program Files\\Zoom\\bin\\Zoom.exe`, "42", "started_at"}},
	} {
		request := httptest.NewRequest(http.MethodGet, test.path, nil)
		response := httptest.NewRecorder()
		server.DesktopHandler().ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("%s HTTP %d: %s", test.path, response.Code, response.Body.String())
		}
		for _, required := range test.required {
			if !strings.Contains(response.Body.String(), required) {
				t.Fatalf("%s response missing %q: %s", test.path, required, response.Body.String())
			}
		}
		if strings.Contains(strings.ToLower(response.Body.String()), "system_fallback") {
			t.Fatalf("%s advertised a silent fallback: %s", test.path, response.Body.String())
		}
	}
}
