package tunnel

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestAuditPreserveRequiredDNSOfFullProfile(t *testing.T) {
	bin := os.Getenv("AIOVERLAY_SINGBOX_BIN")
	if _, err := os.Stat(bin); err != nil {
		t.Skip("host sing-box missing")
	}
	raw := `{"log":{"disabled":true},"dns":{"servers":[{"type":"udp","tag":"vpn-dns","server":"127.0.0.1","server_port":53535}]},"outbounds":[{"type":"vless","server":"vpn.example.invalid","server_port":443,"uuid":"00000000-0000-0000-0000-000000000001","domain_resolver":"vpn-dns","tls":{"enabled":true,"server_name":"vpn.example.invalid"}}]}`
	dir := t.TempDir()
	original := filepath.Join(dir, "original.json")
	generated := filepath.Join(dir, "generated.json")
	os.WriteFile(original, []byte(raw), 0600)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if output, err := exec.CommandContext(ctx, bin, "check", "-c", original).CombinedOutput(); err != nil {
		t.Fatalf("reference profile invalid: %s", output)
	}
	b, err := BuildConfig(raw, 23456, "fake-user", "fake-pass")
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(generated, b, 0600)
	if output, err := exec.CommandContext(ctx, bin, "check", "-c", generated).CombinedOutput(); err != nil {
		t.Fatalf("valid full profile broken by config rewriting: %s", output)
	}
}
func TestAuditProfileEnforcesIPv4OuterDial(t *testing.T) {
	b, err := BuildConfig(`{"type":"vless","server":"vpn.example.invalid","server_port":443,"uuid":"00000000-0000-0000-0000-000000000001"}`, 23456, "fake-user", "fake-pass")
	if err != nil {
		t.Fatal(err)
	}
	var profile map[string]any
	json.Unmarshal(b, &profile)
	// The legacy direct OpenCode CONNECT path explicitly uses tcp4. The VPN branch does not.
	ob := profile["outbounds"].([]any)[0].(map[string]any)
	resolver, _ := ob["domain_resolver"].(map[string]any)
	if resolver["strategy"] != "ipv4_only" && ob["domain_strategy"] != "ipv4_only" {
		t.Fatal("generated tunnel has no explicit IPv4-only outer DNS/dial policy")
	}
}
