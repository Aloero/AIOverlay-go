package config

import (
	"encoding/json"
	"testing"

	"aioverlay/internal/domain"
)

func TestProcessAudioConfigIsExplicitAndCannotDowngradeToEndpoint(t *testing.T) {
	valid := domain.AudioSourceConfig{
		Mode: "process", Process: &domain.AudioProcessConfig{ExecutablePath: `C:\Program Files\Zoom\bin\Zoom.exe`},
		SegmentMS: 1000, SilenceMS: 200, MinimumMS: 80, Threshold: .01,
	}
	if err := ValidateAudio(valid); err != nil {
		t.Fatalf("valid process capture rejected: %v", err)
	}

	withEndpoint := valid
	withEndpoint.DeviceID = "default-render"
	if err := ValidateAudio(withEndpoint); err == nil {
		t.Fatal("process-only capture accepted an endpoint fallback")
	}

	missingProcess := valid
	missingProcess.Process = nil
	if err := ValidateAudio(missingProcess); err == nil {
		t.Fatal("process-only capture accepted no process rule")
	}

	system := valid
	system.Mode = "system"
	if err := ValidateAudio(system); err == nil {
		t.Fatal("system loopback accepted hidden process selection")
	}
}

func TestProcessAudioRequiresAbsoluteExecutablePath(t *testing.T) {
	for _, path := range []string{"Zoom.exe", `Program Files\\Zoom\\Zoom.exe`, `/usr/bin/zoom`} {
		config := domain.AudioSourceConfig{
			Mode: "process", Process: &domain.AudioProcessConfig{ExecutablePath: path},
			SegmentMS: 1000, SilenceMS: 200, MinimumMS: 80, Threshold: .01,
		}
		if err := ValidateAudio(config); err == nil {
			t.Fatalf("relative/non-Windows process path accepted: %q", path)
		}
	}
}

func TestProcessAudioConfigRoundTripPreservesExtensions(t *testing.T) {
	input := domain.AudioSourceConfig{
		Mode: "process", Process: &domain.AudioProcessConfig{
			ExecutablePath: `C:\Program Files\Zoom\bin\Zoom.exe`,
			Extra:          map[string]json.RawMessage{"future_process": json.RawMessage(`{"version":2}`)},
		},
		SegmentMS: 1000, SilenceMS: 200, MinimumMS: 80, Threshold: .01,
		Extra: map[string]json.RawMessage{"future_audio": json.RawMessage(`true`)},
	}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	var output domain.AudioSourceConfig
	if err := json.Unmarshal(raw, &output); err != nil {
		t.Fatal(err)
	}
	if output.Process == nil || output.Process.ExecutablePath != input.Process.ExecutablePath {
		t.Fatalf("process rule lost: %+v", output.Process)
	}
	if string(output.Extra["future_audio"]) != "true" || string(output.Process.Extra["future_process"]) != `{"version":2}` {
		t.Fatalf("extensions lost: audio=%s process=%s", output.Extra["future_audio"], output.Process.Extra["future_process"])
	}
}
