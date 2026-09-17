package providers

import (
	"strings"
	"testing"
)

func TestSplitOpenCodeRef(t *testing.T) {
	p, m := splitOpenCodeRef("anthropic/claude-sonnet", "fallback")
	if p != "anthropic" || m != "claude-sonnet" {
		t.Fatalf("%s %s", p, m)
	}
	p, m = splitOpenCodeRef("mimo-v2.5-free", "opencode")
	if p != "opencode" || m != "mimo-v2.5-free" {
		t.Fatalf("%s %s", p, m)
	}
}
func TestHTTPErrorNormalization(t *testing.T) {
	cases := []struct {
		status int
		msg    string
		want   string
	}{{401, "no", "auth_failed"}, {429, "slow", "rate_limited"}, {403, "not available in your country", "region_restricted"}, {404, "model", "model_unavailable"}, {503, "down", "provider_unavailable"}}
	for _, c := range cases {
		if got := string(HTTPError(c.status, c.msg).Outcome); got != c.want {
			t.Fatalf("%d got %s want %s", c.status, got, c.want)
		}
	}
}

func TestOpenCodeEnvironmentUsesExplicitAIVPNProxy(t *testing.T) {
	s := &openCodeServer{tempDir: t.TempDir(), password: "local", upstreamProxy: "http://u:p@127.0.0.1:12345", credentialEnv: []string{"PROVIDER_API_KEY"}, credential: "provider-secret"}
	joined := strings.Join(s.environment([]byte(`{"provider":{}}`)), "\n")
	for _, want := range []string{"HTTPS_PROXY=http://u:p@127.0.0.1:12345", "HTTP_PROXY=http://u:p@127.0.0.1:12345", "NO_PROXY=127.0.0.1,localhost", "PROVIDER_API_KEY=provider-secret"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q", want)
		}
	}
}

func TestOpenCodeRuntimeKeyChangesWithTunnelEndpoint(t *testing.T) {
	a := openCodeRuntimeKey("same-key", "http://127.0.0.1:10001")
	b := openCodeRuntimeKey("same-key", "http://127.0.0.1:10002")
	if a == b {
		t.Fatal("OpenCode runtime must not survive an AI VPN endpoint change")
	}
}
