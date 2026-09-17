package tunnel

import (
	"context"
	"net/url"
	"os"

	"aioverlay/internal/domain"
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildConfigOwnsLoopbackAndNoSystemProxy(t *testing.T) {
	raw := `{"type":"vless","server":"vpn.example","server_port":443,"uuid":"secret-id","tls":{"enabled":true}}`
	b, err := BuildConfig(raw, 32123, "user-x", "pass-y")
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	text := string(b)
	for _, want := range []string{`"listen": "127.0.0.1"`, `"listen_port": 32123`, `"set_system_proxy": false`, `"final": "ai-vpn"`, `"type": "vless"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %s", want)
		}
	}
	if strings.Contains(text, `"type": "tun"`) {
		t.Fatal("TUN must never be generated")
	}
}

func TestFullConfigCannotInjectInboundOrRoute(t *testing.T) {
	raw := `{"inbounds":[{"type":"tun"}],"route":{"final":"direct"},"outbounds":[{"type":"hysteria2","server":"x","server_port":443,"password":"p"}]}`
	b, err := BuildConfig(raw, 1234, "u", "p")
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	if strings.Contains(text, `"type": "tun"`) || strings.Contains(text, `"final": "direct"`) {
		t.Fatal(text)
	}
	if !strings.Contains(text, `"type": "hysteria2"`) {
		t.Fatal(text)
	}
}

func TestFullConfigRequiresExactlyOneTunnelOutbound(t *testing.T) {
	_, err := BuildConfig(`{"outbounds":[{"type":"vless"},{"type":"trojan"}]}`, 1234, "u", "p")
	if err == nil {
		t.Fatal("expected ambiguity error")
	}
}
func TestDirectIsRejectedAsVPN(t *testing.T) {
	_, err := BuildConfig(`{"type":"direct"}`, 1234, "u", "p")
	if err == nil {
		t.Fatal("direct must not be accepted as VPN")
	}
}

func TestManagerStartsAuthenticatedLoopbackWithOverride(t *testing.T) {
	if os.Getenv("AIOVERLAY_SINGBOX_BIN") == "" {
		t.Skip("set AIOVERLAY_SINGBOX_BIN to exercise a host sing-box binary")
	}
	cfg := domain.VPNConfig{Enabled: true, Engine: "sing-box", Config: json.RawMessage(`{"type":"vless","server":"example.com","server_port":443,"uuid":"00000000-0000-0000-0000-000000000001","tls":{"enabled":true,"server_name":"example.com"}}`)}
	m := New(cfg)
	defer m.Close()
	raw, err := m.ProxyURL(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() != "127.0.0.1" || u.User == nil {
		t.Fatalf("unexpected proxy URL %q: %v", raw, err)
	}
	if _, ok := u.User.Password(); !ok {
		t.Fatal("loopback proxy must require a password")
	}
}
