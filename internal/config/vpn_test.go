package config

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"aioverlay/internal/domain"
)

func TestVPNDisabledByDefaultWhenMissingOrZero(t *testing.T) {
	cfg := Default()
	cfg.VPN = domain.VPNConfig{}
	if err := Validate(cfg); err != nil {
		t.Fatal(err)
	}
}

func TestVPNEnabledRequiresJSONConfig(t *testing.T) {
	cfg := Default()
	cfg.VPN = domain.VPNConfig{Enabled: true, Engine: "sing-box"}
	if err := Validate(cfg); err == nil {
		t.Fatal("expected missing VPN config error")
	}
	cfg.VPN.Config = json.RawMessage(`{"type":"vless","server":"example","server_port":443,"uuid":"x"}`)
	if err := Validate(cfg); err != nil {
		t.Fatal(err)
	}
}

func TestVPNUnknownEngineRejectedOnlyWhenEnabled(t *testing.T) {
	cfg := Default()
	cfg.VPN = domain.VPNConfig{Enabled: false, Engine: "future-engine"}
	if err := Validate(cfg); err != nil {
		t.Fatal(err)
	}
	cfg.VPN.Enabled = true
	cfg.VPN.Config = json.RawMessage(`{}`)
	if err := Validate(cfg); err == nil {
		t.Fatal("expected engine validation error")
	}
}

func TestDecodeOldConfigWithoutVPNDefaultsDisabled(t *testing.T) {
	cfg := Default()
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatal(err)
	}
	delete(obj, "vpn")
	raw, _ = json.Marshal(obj)
	got, err := Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if got.VPN.Enabled {
		t.Fatal("missing vpn must default disabled")
	}
}

func TestVPNConfigRoundTripAsNestedJSON(t *testing.T) {
	cfg := Default()
	cfg.VPN = domain.VPNConfig{Enabled: true, Engine: "sing-box", Config: json.RawMessage(`{"type":"hysteria2","server":"vpn.example","server_port":443,"password":"secret"}`)}
	path := filepath.Join(t.TempDir(), "config.json")
	store := NewStore(path)
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(`\"type\"`)) {
		t.Fatal("vpn config was serialized as an escaped JSON string")
	}
	if !bytes.Contains(raw, []byte(`"type": "hysteria2"`)) {
		t.Fatalf("nested vpn JSON missing: %s", raw)
	}
	got, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	var vpn map[string]any
	if err := json.Unmarshal(got.VPN.Config, &vpn); err != nil {
		t.Fatal(err)
	}
	if vpn["password"] != "secret" {
		t.Fatal("vpn config not preserved")
	}
}
