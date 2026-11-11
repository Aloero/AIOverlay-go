package opencode

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aioverlay/internal/childprocess"
	"aioverlay/internal/modelhost"
)

func TestEnvironmentUsesExplicitAIVPNProxy(t *testing.T) {
	i := &instance{
		host:          New(Config{CredentialEnv: []string{"PROVIDER_API_KEY"}}),
		credential:    "provider-secret",
		upstreamProxy: "http://u:p@127.0.0.1:12345",
		tempDir:       t.TempDir(),
		password:      "local",
	}
	joined := strings.Join(i.environment([]byte(`{"provider":{}}`)), "\n")
	for _, want := range []string{
		"HTTPS_PROXY=http://u:p@127.0.0.1:12345", "HTTP_PROXY=http://u:p@127.0.0.1:12345",
		"NO_PROXY=127.0.0.1,localhost", "PROVIDER_API_KEY=provider-secret",
		"OPENCODE_SERVER_PASSWORD=local",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q", want)
		}
	}
	// The child gets an isolated profile: nothing from the surrounding user may leak in.
	for _, leak := range []string{"OPENCODE_CONFIG=", "OPENCODE_CONFIG_DIR="} {
		if strings.Contains(joined, leak) {
			t.Fatalf("host environment leaked %q", leak)
		}
	}
}

func TestEnvironmentCarriesOutputTokenCap(t *testing.T) {
	i := &instance{host: New(Config{}), tempDir: t.TempDir(), outputTokenCap: 123}
	env := strings.Join(i.environment(nil), "\n")
	if !strings.Contains(env, "OPENCODE_EXPERIMENTAL_OUTPUT_TOKEN_MAX=123") {
		t.Fatalf("output cap was not forwarded:\n%s", env)
	}
}

func TestEnvironmentPinsOnlyAUsableCatalog(t *testing.T) {
	dir := t.TempDir()
	snapshot := filepath.Join(dir, "models-dev-api.json")
	if err := os.WriteFile(snapshot, make([]byte, 2<<20), 0o600); err != nil {
		t.Fatal(err)
	}
	pinned := strings.Join((&instance{host: New(Config{}), tempDir: dir, password: "local", catalogPath: snapshot}).environment(nil), "\n")
	if !strings.Contains(pinned, "OPENCODE_DISABLE_MODELS_FETCH=1") || !strings.Contains(pinned, "OPENCODE_MODELS_PATH="+snapshot) {
		t.Fatalf("a usable snapshot must pin the runtime catalog:\n%s", pinned)
	}
	small := filepath.Join(dir, "small.json")
	if err := os.WriteFile(small, []byte(`{"opencode":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// A missing or truncated file must not hide every model from the runtime: OPENCODE_DISABLE_
	// MODELS_FETCH without a usable path leaves the runtime with an empty catalog.
	for _, tc := range []struct{ name, path string }{
		{"no snapshot", ""},
		{"absent file", filepath.Join(dir, "absent.json")},
		{"placeholder", small},
	} {
		env := strings.Join((&instance{host: New(Config{}), tempDir: dir, password: "local", catalogPath: tc.path}).environment(nil), "\n")
		if strings.Contains(env, "OPENCODE_MODELS_PATH") || strings.Contains(env, "OPENCODE_DISABLE_MODELS_FETCH") {
			t.Fatalf("%s: the runtime must keep its own default:\n%s", tc.name, env)
		}
	}
}

func TestKeySeparatesInstancesThatCannotBeShared(t *testing.T) {
	host := New(Config{})
	base := modelhost.Need{Credential: "same-key", Proxy: "http://127.0.0.1:10001"}
	if host.key(base) == host.key(modelhost.Need{Credential: "same-key", Proxy: "http://127.0.0.1:10002"}) {
		t.Fatal("a runtime must not survive an AI VPN endpoint change")
	}
	if host.key(base) == host.key(modelhost.Need{Credential: "other-key", Proxy: base.Proxy}) {
		t.Fatal("a runtime must not be shared between credentials")
	}
	if host.key(base) == host.key(modelhost.Need{Credential: base.Credential, Proxy: base.Proxy, OutputTokenCap: 256}) {
		t.Fatal("a boot-time output cap change must not reuse the old process")
	}
}

// A catalog refresh replaces the snapshot the warm runtime booted with, so the instance must not be
// reused across it - otherwise "model not found" comes back through the warm path.
func TestKeyChangesWithCatalogGeneration(t *testing.T) {
	dir := t.TempDir()
	snapshot := filepath.Join(dir, "models-dev-api.json")
	if err := os.WriteFile(snapshot, []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	host := New(Config{})
	need := modelhost.Need{Catalog: snapshot}
	before := host.key(need)
	if before != host.key(need) {
		t.Fatal("the same snapshot must keep the runtime warm")
	}
	if err := os.WriteFile(snapshot, []byte("second, refreshed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if before == host.key(need) {
		t.Fatal("a replaced snapshot must retire the warm runtime")
	}
	if catalogGeneration(filepath.Join(dir, "absent.json")) != "unpinned" {
		t.Fatal("a missing snapshot must not pin a generation")
	}
}

func TestVersionComesFromTheShippedIdentityFile(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "opencode.exe")
	if err := os.WriteFile(bin, []byte("not really a binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "VERSION.txt"), []byte("OpenCode v1.18.31\nOfficial release: https://example.invalid\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := New(Config{Binary: bin}).Version(); got != "1.18.31" {
		t.Fatalf("version=%q", got)
	}
	// No identity file means unknown, never a guess.
	os.Remove(filepath.Join(dir, "VERSION.txt"))
	if got := New(Config{Binary: bin}).Version(); got != "" {
		t.Fatalf("version=%q, want empty", got)
	}
}

func TestExitedProcessIsNotReportedAlive(t *testing.T) {
	if os.Getenv("AIO_HOST_CHILD") == "1" {
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestExitedProcessIsNotReportedAlive$")
	cmd.Env = append(os.Environ(), "AIO_HOST_CHILD=1")
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
	if (&instance{process: proc}).Alive() {
		t.Fatal("an exited runtime was offered as alive")
	}
}

func TestServedIsExplicitlyUnknown(t *testing.T) {
	// Reporting "no models" would hide every model from the UI; the host must say it cannot tell.
	if _, err := New(Config{}).Served(context.Background(), modelhost.Need{}); !errors.Is(err, modelhost.ErrServedUnsupported) {
		t.Fatalf("err=%v", err)
	}
}
