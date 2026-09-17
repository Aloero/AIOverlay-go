package tunnel

import (
	"aioverlay/internal/domain"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestImportSupportedShareLinks(t *testing.T) {
	for _, raw := range []string{
		"vless://00000000-0000-0000-0000-000000000001@vpn.example:443?security=tls&type=ws&path=%2Fstream&host=cdn.example#Primary",
		"hysteria2://user:p%40ss@vpn.example/?obfs=salamander&obfs-password=synthetic&sni=tls.example#QUIC",
		"hy2://test@vpn.example:443/?sni=vpn.example",
		"trojan://synthetic@vpn.example:443?type=grpc&serviceName=api&sni=vpn.example",
	} {
		p, err := ImportProfile(raw)
		if err != nil {
			t.Fatal(raw, err)
		}
		var obj map[string]any
		if json.Unmarshal(p.Config, &obj) != nil || p.Server != "vpn.example" {
			t.Fatal(p)
		}
		if os.Getenv("AIOVERLAY_SINGBOX_BIN") != "" {
			if err := Validate(context.Background(), domain.VPNConfig{Enabled: true, Engine: "sing-box", Config: p.Config}); err != nil {
				t.Fatal(err)
			}
		}
	}
}
func TestImportNeverSilentlyDropsSecurity(t *testing.T) {
	for _, raw := range []string{
		"vless://00000000-0000-0000-0000-000000000001@vpn.example:443?security=tls&encryption=new-unsupported",
		"hy2://synthetic-secret@vpn.example/?pinSHA256=secret-fingerprint",
		"hy2://synthetic-secret@vpn.example/?sni=one&sni=two",
		"https://synthetic-secret@subscription.example/config",
		"vless://00000000-0000-0000-0000-000000000001@vpn.example:443?security=reality",
	} {
		_, err := ImportProfile(raw)
		if err == nil {
			t.Fatal("unsafe import accepted")
		}
		if strings.Contains(err.Error(), "synthetic-secret") {
			t.Fatal("URI secret leaked")
		}
	}
}
func TestVPNProbeDisabledNeverConnects(t *testing.T) {
	_, err := New(domain.VPNConfig{}).Probe(context.Background(), "https://example.com")
	if err == nil {
		t.Fatal("disabled VPN probe allowed")
	}
}
