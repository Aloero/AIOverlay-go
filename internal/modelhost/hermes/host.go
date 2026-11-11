// Package hermes is the model host for the Hermes Agent harness (Nous Research). The harness
// is not a server: every attempt runs as a one-shot CLI call (`hermes chat --safe-mode …`), so
// the host owns only what outlives a call - locating the executable, reporting its version, and
// keeping an isolated HERMES_HOME so the app never reads or writes the user's own Hermes profile
// or credentials.
package hermes

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"aioverlay/internal/modelhost"
)

// HostID is what an Endpoint reports as its implementation, so traces name the harness that
// actually answered.
const HostID = "hermes"

type Config struct {
	// Binary overrides the executable lookup (tests, non-PATH installs).
	Binary string
	// Home overrides the isolated HERMES_HOME directory.
	Home string
}

type Host struct {
	cfg Config

	mu      sync.Mutex
	homeDir string
	version string
	probed  bool
}

func New(cfg Config) *Host {
	if cfg.Binary == "" {
		cfg.Binary = strings.TrimSpace(os.Getenv("AIOVERLAY_HERMES_BIN"))
	}
	return &Host{cfg: cfg}
}

func (h *Host) ID() string { return HostID }

// Executable resolves the hermes binary. On Windows the installer may expose `hermes.exe` or a
// `hermes.cmd` shim; LookPath consults PATHEXT for the bare name first, so only the .cmd case
// needs a fallback.
func (h *Host) Executable() (string, error) {
	name := h.cfg.Binary
	if name == "" {
		name = "hermes"
	}
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}
	if p, err := exec.LookPath(name + ".cmd"); err == nil {
		return p, nil
	}
	return "", errors.New("Hermes is not installed or not on PATH")
}

// Home returns the isolated profile directory, creating it on first use. A one-shot safe-mode
// run reads no user profile, but the harness still wants a writable home for its gateway and
// session bookkeeping; giving it a private one keeps our runs out of the user's history.
func (h *Host) Home() (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.homeDir != "" {
		return h.homeDir, nil
	}
	if h.cfg.Home != "" {
		h.homeDir = h.cfg.Home
		return h.homeDir, nil
	}
	dir, err := os.MkdirTemp("", "aioverlay-hermes-")
	if err != nil {
		return "", err
	}
	h.homeDir = dir
	return dir, nil
}

// Version is probed once (`hermes --version`) and cached: it changes only with a harness update,
// and every attempt does not need to pay a process launch for a label.
func (h *Host) Version() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.probed {
		return h.version
	}
	h.probed = true
	path, err := h.Executable()
	if err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	// A .cmd shim cannot be launched directly (CreateProcess); the version probe must route it
	// through cmd.exe the same way the request transport does.
	cmd := exec.CommandContext(ctx, path, "--version")
	if ext := strings.ToLower(filepath.Ext(path)); ext == ".cmd" || ext == ".bat" {
		cmd = exec.CommandContext(ctx, "cmd.exe", "/d", "/c", path, "--version")
	}
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0])
	if len(line) > 60 {
		line = line[:60]
	}
	h.version = line
	return h.version
}

// Endpoint reports that the host is usable right now. There is no server to wait for, so this
// never blocks beyond the executable lookup.
func (h *Host) Endpoint(ctx context.Context, need modelhost.Need) (modelhost.Endpoint, func(), error) {
	if _, err := h.Executable(); err != nil {
		return modelhost.Endpoint{}, nil, err
	}
	if _, err := h.Home(); err != nil {
		return modelhost.Endpoint{}, nil, err
	}
	return modelhost.Endpoint{Host: HostID, Version: h.Version(), Proxy: need.Proxy}, func() {}, nil
}

// Served returns ErrServedUnsupported: a custom-endpoint harness does not publish a stable model
// list the way the OpenCode control API does; availability is decided by the upstream endpoint.
func (h *Host) Served(context.Context, modelhost.Need) ([]modelhost.ModelRef, error) {
	return nil, modelhost.ErrServedUnsupported
}

// Close removes the private profile directory, mirroring the session cleanup rule the OpenCode
// adapter follows: best-effort, never an assurance that the harness wrote nothing anywhere.
func (h *Host) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.homeDir != "" && h.cfg.Home == "" && !filepath.IsAbs(h.cfg.Home) {
		_ = os.RemoveAll(h.homeDir)
		h.homeDir = ""
	}
}
