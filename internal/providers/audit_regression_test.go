package providers

import (
	"aioverlay/internal/domain"
	"strings"
	"testing"
	"time"
)

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
