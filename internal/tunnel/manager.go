package tunnel

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"aioverlay/internal/childprocess"
	"aioverlay/internal/domain"
)

type Manager struct {
	mu          sync.Mutex
	cfg         domain.VPNConfig
	runtime     *singBoxRuntime
	closed      bool
	starting    chan struct{}
	startCancel context.CancelFunc
	lastError   string
}

type singBoxRuntime struct {
	cmd        *exec.Cmd
	process    *childprocess.Process
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
	for {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		m.mu.Lock()
		if m.closed {
			m.mu.Unlock()
			return "", fmt.Errorf("AI VPN is closed")
		}
		if m.starting != nil {
			done := m.starting
			m.mu.Unlock()
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-done:
			}
			continue
		}
		if r := m.runtime; r != nil && r.alive() {
			raw := r.proxyURL
			m.mu.Unlock()
			return raw, nil
		}
		old := m.runtime
		m.runtime = nil
		done := make(chan struct{})
		m.starting = done
		startCtx, cancel := context.WithCancel(ctx)
		m.startCancel = cancel
		m.mu.Unlock()
		if old != nil {
			old.stop()
		}
		r, err := startSingBox(startCtx, m.cfg)
		cancel()
		m.mu.Lock()
		if m.closed && err == nil {
			err = fmt.Errorf("AI VPN closed during startup")
		}
		if err == nil {
			m.runtime = r
			m.lastError = ""
		} else {
			m.lastError = err.Error()
		}
		m.starting = nil
		m.startCancel = nil
		close(done)
		m.mu.Unlock()
		if err != nil {
			if r != nil {
				r.stop()
			}
			return "", err
		}
		return r.proxyURL, nil
	}
}
func (m *Manager) Close() {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.closed = true
	r := m.runtime
	m.runtime = nil
	cancel, done := m.startCancel, m.starting
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
	if r != nil {
		r.stop()
	}
}
func (m *Manager) Status() map[string]any {
	if m == nil {
		return map[string]any{"enabled": false}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return map[string]any{"enabled": m.cfg.Enabled, "running": m.runtime != nil && m.runtime.alive(), "starting": m.starting != nil, "error": m.lastError, "scope": "AI provider traffic only; not an OS egress sandbox", "outer_ip": "IPv4"}
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
	u, err := randomToken(8)
	if err != nil {
		return nil, err
	}
	username := "aioverlay-" + u
	password, err := randomToken(24)
	if err != nil {
		return nil, err
	}
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
	proc, err := childprocess.Start(cmd)
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("start embedded sing-box: %w", err)
	}
	proxy := (&url.URL{Scheme: "http", Host: fmt.Sprintf("127.0.0.1:%d", port), User: url.UserPassword(username, password)}).String()
	r := &singBoxRuntime{cmd: cmd, process: proc, configPath: configPath, proxyURL: proxy}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if proxyAuthenticated(ctx, proxy) && r.alive() {
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
	return r != nil && r.process.Alive()
}
func (r *singBoxRuntime) stop() {
	if r == nil {
		return
	}
	r.process.Stop()
	if r.configPath != "" {
		if err := os.RemoveAll(filepath.Dir(r.configPath)); err != nil {
			log.Printf("VPN temporary config cleanup failed: %v", err)
		}
	}
	r.cmd = nil
}

// Verify the private SOCKS5 username/password handshake. Merely finding any
// listener on the released port is not proof that our proxy started.
func proxyAuthenticated(ctx context.Context, raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return false
	}
	username := u.User.Username()
	password, ok := u.User.Password()
	if !ok || len(username) > 255 || len(password) > 255 {
		return false
	}
	conn, err := (&net.Dialer{Timeout: 120 * time.Millisecond}).DialContext(ctx, "tcp4", u.Host)
	if err != nil {
		return false
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(200 * time.Millisecond))
	if _, err = conn.Write([]byte{5, 1, 2}); err != nil {
		return false
	}
	reply := make([]byte, 2)
	if _, err = io.ReadFull(conn, reply); err != nil || reply[0] != 5 || reply[1] != 2 {
		return false
	}
	auth := append([]byte{1, byte(len(username))}, []byte(username)...)
	auth = append(auth, byte(len(password)))
	auth = append(auth, []byte(password)...)
	if _, err = conn.Write(auth); err != nil {
		return false
	}
	_, err = io.ReadFull(conn, reply)
	return err == nil && reply[0] == 1 && reply[1] == 0
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
func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("OS random source unavailable for private VPN credentials")
	}
	return hex.EncodeToString(b), nil
}

func findSingBoxBinary() (string, error) {
	if override := strings.TrimSpace(os.Getenv("AIOVERLAY_SINGBOX_BIN")); override != "" {
		if st, err := os.Stat(override); err == nil && !st.IsDir() {
			return filepath.Abs(override)
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
			return filepath.Abs(p)
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
