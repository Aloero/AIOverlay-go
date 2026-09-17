package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"aioverlay/internal/config"
	"aioverlay/internal/engine"
)

func TestPipelineBehaviorPreviewIsPureAndUsesDraft(t *testing.T) {
	backend := &auditBackend{cfg: config.Default(), started: make(chan struct{}, 1)}
	server := New("127.0.0.1:0", backend)
	pipeline := backend.cfg.Pipelines[0]
	pipeline.Name = "Draft behavior"
	body, err := json.Marshal(map[string]any{"pipeline": pipeline})
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, "/api/pipeline/behavior", strings.NewReader(string(body)))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.DesktopHandler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("HTTP %d: %s", response.Code, response.Body.String())
	}

	var report engine.BehaviorReport
	if err := json.Unmarshal(response.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.PipelineID != pipeline.ID || len(report.Commands) != 1 || report.Commands[0].Capture != "screen" {
		t.Fatalf("report=%+v", report)
	}
	select {
	case <-backend.started:
		t.Fatal("behavior preview started model execution")
	default:
	}
}
