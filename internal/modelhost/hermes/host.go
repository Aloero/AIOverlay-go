// Package hermes is the model host for the Hermes Agent harness (Nous Research). The harness
// is not a server: every attempt runs as a one-shot CLI call (`hermes chat --safe-mode …`), so
// the host owns only what outlives a call - locating the executable, reporting its version, and
// keeping an isolated HERMES_HOME so the app never reads or writes the user's own Hermes profile
// or credentials.
package hermes

import (
	"context"
	"errors"
	"fmt"
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

// Executable resolves the hermes binary in a fixed order:
//  1. an explicit override (Config.Binary, seeded from AIOVERLAY_HERMES_BIN) - tests and
//     non-standard installs point straight at the file;
//  2. the runtime/hermes bundle the release archive ships next to the executable, the same
//     layout as the embedded OpenCode runtime, so the connector is used without any user
//     install; the executable's directory wins because the overlay also starts from Task
//     Scheduler, whose working directory is not the app folder, then a relative path for tests;
//  3. PATH, then the per-user install.ps1 layout (%LOCALAPPDATA%\hermes\bin) for a machine that
//     installed Hermes outside our bundle.
//
// On Windows the harness may be exposed as `hermes.exe` or a `hermes.cmd` shim; LookPath
// consults PATHEXT for the bare name, so only the direct-path checks need both spellings.
func (h *Host) Executable() (string, error) {
	if override := strings.TrimSpace(h.cfg.Binary); override != "" {
		for _, name := range []string{override, override + ".cmd"} {
			if st, err := os.Stat(name); err == nil && !st.IsDir() {
				return filepath.Abs(name)
			}
		}
		if p, err := exec.LookPath(override); err == nil {
			return p, nil
		}
		// An explicit override is a statement of intent: falling back to another copy would
		// hide a typo or a removed install, so an unusable override is the error.
		return "", fmt.Errorf("configured Hermes executable %q is not usable", override)
	}
	if p, ok := h.bundled(); ok {
		return p, nil
	}
	for _, p := range []string{"hermes", "hermes.cmd"} {
		if found, err := exec.LookPath(p); err == nil {
			return found, nil
		}
	}
	if local := localInstallDir(); local != "" {
		for _, name := range []string{"hermes.exe", "hermes.cmd", "hermes"} {
			p := filepath.Join(local, name)
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				return p, nil
			}
		}
	}
	return "", errors.New("Hermes is not installed: bundle it under runtime/hermes or put it on PATH")
}

// bundled finds the runtime/hermes executable shipped next to the app, mirroring the OpenCode
// host's lookup so a packaged release is self-contained.
func (h *Host) bundled() (string, bool) {
	candidates := []string{}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(dir, "runtime", "hermes", "hermes.exe"),
			filepath.Join(dir, "runtime", "hermes", "hermes.cmd"),
			filepath.Join(dir, "runtime", "hermes", "hermes"))
	}
	candidates = append(candidates,
		filepath.Join("runtime", "hermes", "hermes.exe"),
		filepath.Join("runtime", "hermes", "hermes.cmd"),
		filepath.Join("runtime", "hermes", "hermes"))
	for _, p := range candidates {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			if abs, err := filepath.Abs(p); err == nil {
				return abs, true
			}
			return p, true
		}
	}
	return "", false
}

// localInstallDir is where the PowerShell installer drops the CLI entrypoint; empty when the
// environment variable is absent (non-Windows, or a stripped environment).
func localInstallDir() string {
	if root := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); root != "" {
		return filepath.Join(root, "hermes", "bin")
	}
	return ""
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
