package tunnel

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"aioverlay/internal/domain"
)

type Manager struct {
	mu      sync.Mutex
	cfg     domain.VPNConfig
	runtime *singBoxRuntime
	closed  bool
}

type singBoxRuntime struct {
	cmd        *exec.Cmd
	configPath string
	proxyURL   string
}

func New(cfg domain.VPNConfig) *Manager { return &Manager{cfg: cfg} }

func (m *Manager) Enabled() bool { return m != nil && m.cfg.Enabled }

// ProxyURL is lazy. An enabled tunnel fails closed: callers receive an error
// instead of silently falling back to direct networking.
func (m *Manager) ProxyURL(ctx context.Context) (string, error) {
	if m == nil || !m.cfg.Enabled {
		return "", nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return "", fmt.Errorf("AI VPN is closed")
	}
	if m.runtime != nil && m.runtime.alive() && proxyListening(m.runtime.proxyURL) {
		return m.runtime.proxyURL, nil
	}
	if m.runtime != nil {
		m.runtime.stop()
		m.runtime = nil
	}
	r, err := startSingBox(ctx, m.cfg)
	if err != nil {
		return "", err
	}
	m.runtime = r
	return r.proxyURL, nil
}

func (m *Manager) Close() {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.closed = true
	r := m.runtime
	m.runtime = nil
	m.mu.Unlock()
	if r != nil {
		r.stop()
	}
}

func startSingBox(ctx context.Context, cfg domain.VPNConfig) (*singBoxRuntime, error) {
	engine := strings.TrimSpace(cfg.Engine)
	if engine == "" {
		engine = EngineSingBox
	}
	if engine != EngineSingBox {
		return nil, fmt.Errorf("unsupported AI VPN engine %q", engine)
	}
	bin, err := findSingBoxBinary()
	if err != nil {
		return nil, err
	}
	port, err := reserveLoopbackPort()
	if err != nil {
		return nil, fmt.Errorf("reserve AI VPN port: %w", err)
	}
	username := "aioverlay-" + randomToken(8)
	password := randomToken(24)
	generated, err := BuildConfig(string(cfg.Config), port, username, password)
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "aioverlay-singbox-")
	if err != nil {
		return nil, err
	}
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, generated, 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	if err := checkConfig(ctx, bin, configPath); err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	cmd := exec.Command(bin, "run", "-c", configPath)
	configureHiddenChild(cmd)
	cmd.Dir = filepath.Dir(bin)
	cmd.Env = minimalProcessEnv()
	if err := cmd.Start(); err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("start embedded sing-box: %w", err)
	}
	proxy := (&url.URL{Scheme: "http", Host: fmt.Sprintf("127.0.0.1:%d", port), User: url.UserPassword(username, password)}).String()
	r := &singBoxRuntime{cmd: cmd, configPath: configPath, proxyURL: proxy}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, dialErr := (&net.Dialer{Timeout: 120 * time.Millisecond}).DialContext(ctx, "tcp4", fmt.Sprintf("127.0.0.1:%d", port))
		if dialErr == nil {
			_ = conn.Close()
			return r, nil
		}
		if !r.alive() {
			r.stop()
			return nil, fmt.Errorf("embedded sing-box exited before its loopback proxy became ready")
		}
		select {
		case <-ctx.Done():
			r.stop()
			return nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	r.stop()
	return nil, fmt.Errorf("embedded sing-box loopback proxy did not become ready")
}

func checkConfig(ctx context.Context, bin, path string) error {
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(checkCtx, bin, "check", "-c", path)
	configureHiddenChild(cmd)
	cmd.Dir = filepath.Dir(bin)
	cmd.Env = minimalProcessEnv()
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("sing-box rejected the AI VPN configuration")
	}
	return nil
}

func (r *singBoxRuntime) alive() bool {
	return r != nil && r.cmd != nil && r.cmd.Process != nil && r.cmd.ProcessState == nil
}
func (r *singBoxRuntime) stop() {
	if r == nil {
		return
	}
	if r.cmd != nil && r.cmd.Process != nil {
		_ = r.cmd.Process.Kill()
		_, _ = r.cmd.Process.Wait()
	}
	if r.configPath != "" {
		_ = os.RemoveAll(filepath.Dir(r.configPath))
	}
	r.cmd = nil
}

func proxyListening(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return false
	}
	conn, err := net.DialTimeout("tcp4", u.Host, 80*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func reserveLoopbackPort() (int, error) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port, nil
}
func randomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

func findSingBoxBinary() (string, error) {
	if override := strings.TrimSpace(os.Getenv("AIOVERLAY_SINGBOX_BIN")); override != "" {
		if st, err := os.Stat(override); err == nil && !st.IsDir() {
			return override, nil
		}
		return "", fmt.Errorf("AIOVERLAY_SINGBOX_BIN does not point to a file")
	}
	var candidates []string
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), "runtime", "sing-box", "sing-box.exe"))
	}
	candidates = append(candidates, filepath.Join("runtime", "sing-box", "sing-box.exe"))
	for _, p := range candidates {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	return "", fmt.Errorf("embedded sing-box runtime not found")
}

func minimalProcessEnv() []string {
	allowed := map[string]bool{"PATH": true, "PATHEXT": true, "SYSTEMROOT": true, "WINDIR": true, "COMSPEC": true, "SYSTEMDRIVE": true, "TEMP": true, "TMP": true, "SSL_CERT_FILE": true, "SSL_CERT_DIR": true}
	out := []string{}
	for _, item := range os.Environ() {
		name, _, _ := strings.Cut(item, "=")
		if allowed[strings.ToUpper(name)] {
			out = append(out, item)
		}
	}
	return out
}

// Validate checks the safe generated profile. On Windows (or when a test binary
// override is supplied) it also runs `sing-box check`; on other development
// hosts it validates structure without trying to execute the bundled Windows EXE.
func Validate(ctx context.Context, cfg domain.VPNConfig) error {
	if !cfg.Enabled {
		return nil
	}
	generated, err := BuildConfig(string(cfg.Config), 23456, "validation-user", "validation-pass")
	if err != nil {
		return err
	}
	if runtime.GOOS != "windows" && strings.TrimSpace(os.Getenv("AIOVERLAY_SINGBOX_BIN")) == "" {
		return nil
	}
	bin, err := findSingBoxBinary()
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "aioverlay-singbox-check-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, generated, 0o600); err != nil {
		return err
	}
	return checkConfig(ctx, bin, path)
}
