package providers

import (
	"aioverlay/internal/childprocess"
	"aioverlay/internal/domain"
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestAuditInvalidGeminiURLReturnsErrorNotPanic(t *testing.T) {
	defer func() {
		if v := recover(); v != nil {
			t.Fatalf("malformed URL panicked instead of normalized error: %v", v)
		}
	}()
	p := NewGemini(domain.ProviderConfig{ID: "gemini", BaseURL: "http://["})
	events := make(chan Event, 16)
	if err := p.stream(context.Background(), Request{ModelID: "test"}, "fake", events); err == nil {
		t.Fatal("expected validation error")
	}
}
func TestAuditExitedOpenCodeNotAlive(t *testing.T) {
	if os.Getenv("AIO_AUDIT_CHILD") == "1" {
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestAuditExitedOpenCodeNotAlive$")
	cmd.Env = append(os.Environ(), "AIO_AUDIT_CHILD=1")
	proc, err := childprocess.Start(cmd)
	if err != nil {
		t.Fatal(err)
	}
	defer proc.Stop()
	select {
	case <-proc.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("child did not exit")
	}
	s := &openCodeServer{cmd: cmd, process: proc}
	if s.alive() {
		t.Fatal("dead OpenCode runtime reused")
	}
}

func TestAuditHTTPErrorRedactionDoesNotLeakPartialSecret(t *testing.T) {
	secret := "sk-fake-" + strings.Repeat("x", 72)
	err := HTTPError(401, strings.Repeat("A", 360)+secret)
	// Same replacement boundary used by engine.safeError and runner.emit.
	safe := strings.ReplaceAll(err.Error(), secret, "[redacted]")
	if strings.Contains(safe, secret[:24]) {
		t.Fatal("truncation occurred before redaction; a 24-byte secret prefix remain visible")
	}
}
func TestAuditGeminiAuthNormalization(t *testing.T) {
	err := HTTPError(400, `{"error":{"code":400,"message":"API key not valid. Please pass a valid API key.","status":"INVALID_ARGUMENT","details":[{"reason":"API_KEY_INVALID"}]}}`)
	if err.Outcome != domain.OutcomeAuthFailed {
		t.Fatalf("structured invalid key mapped to %s", err.Outcome)
	}
}

var _ = time.Second
