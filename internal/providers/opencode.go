package providers

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"aioverlay/internal/domain"
)

type EmbeddedOpenCode struct {
	cfg      domain.ProviderConfig
	proxy    ProxyResolver
	runtimes credentialRuntimes
}

func NewEmbeddedOpenCode(cfg domain.ProviderConfig, resolver ...ProxyResolver) *EmbeddedOpenCode {
	var proxy ProxyResolver
	if len(resolver) > 0 {
		proxy = resolver[0]
	}
	return &EmbeddedOpenCode{cfg: cfg, proxy: proxy}
}
func (p *EmbeddedOpenCode) Incremental() bool { return false }

func (p *EmbeddedOpenCode) ID() string { return p.cfg.ID }

func (p *EmbeddedOpenCode) Stream(ctx context.Context, r Request, credential string) (<-chan Event, <-chan error) {
	events := make(chan Event, 4)
	errs := make(chan error, 1)
	go func() {
		defer close(events)
		defer close(errs)
		events <- Event{Type: EventStart, ModelID: r.ModelID}
		text, err := p.run(ctx, r, credential)
		if err != nil {
			errs <- err
			return
		}
		events <- Event{Type: EventFirstToken, ModelID: r.ModelID}
		events <- Event{Type: EventDelta, Text: text, ModelID: r.ModelID}
		events <- Event{Type: EventDone, ModelID: r.ModelID}
	}()
	return events, errs
}

func (p *EmbeddedOpenCode) run(ctx context.Context, r Request, credential string) (string, error) {
	providerID, modelID := splitOpenCodeRef(r.ModelID, p.cfg.OpenCodeProviderID)
	expected := p.cfg.OpenCodeProviderID
	if expected == "" {
		expected = "opencode"
	}
	if providerID != expected {
		return "", &RequestError{Outcome: domain.OutcomeValidationFailed, Message: "Model provider does not match the configured OpenCode connection"}
	}
	upstreamProxy := ""
	var err error
	if p.proxy != nil {
		upstreamProxy, err = p.proxy.ProxyURL(ctx)
		if err != nil {
			return "", &RequestError{Outcome: domain.OutcomeNetworkError, Message: "AI VPN unavailable: " + err.Error(), Retryable: true}
		}
	}
	if upstreamProxy == "" {
		upstreamProxy, err = ipv4ProxyURL()
		if err != nil {
			return "", fmt.Errorf("IPv4 proxy: %w", err)
		}
	}
	runtimeKey := openCodeRuntimeKey(credential, upstreamProxy)
	instance, release, err := p.runtimes.acquire(ctx, runtimeKey, func(ctx context.Context, _ string) (runtimeInstance, error) {
		srv := &openCodeServer{providerID: expected, credentialEnv: p.cfg.CredentialEnv, credential: credential, baseURL: p.cfg.BaseURL, upstreamProxy: upstreamProxy}
		if err := srv.start(ctx); err != nil {
			srv.stop()
			return nil, err
		}
		return srv, nil
	})
	if err != nil {
		return "", err
	}
	defer release()
	srv := instance.(*openCodeServer)
	parts := []any{map[string]any{"type": "text", "text": r.UserPrompt}}
	if len(r.ImageJPEG) > 0 {
		parts = append(parts, map[string]any{"type": "file", "mime": "image/jpeg", "filename": "screen.jpg", "url": "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(r.ImageJPEG)})
	}
	sid, err := srv.createSession(ctx)
	if err != nil {
		return "", err
	}
	// Delete the session after each request. This does not promise the third-party
	// runtime never writes session data, but prevents unbounded screenshot history.
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if ctx.Err() != nil {
			_ = srv.doJSON(cleanup, http.MethodPost, "/session/"+sid+"/abort", nil, nil, time.Second)
		}
		_ = srv.doJSON(cleanup, http.MethodDelete, "/session/"+sid, nil, nil, time.Second)
	}()
	payload := map[string]any{"model": map[string]any{"providerID": providerID, "modelID": modelID}, "system": r.SystemPrompt, "parts": parts, "tools": map[string]bool{"*": false}}
	var resp struct {
		Info struct {
			Error json.RawMessage `json:"error"`
		} `json:"info"`
		Parts []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"parts"`
	}
	if err := srv.doJSON(ctx, http.MethodPost, "/session/"+sid+"/message", payload, &resp, r.ResponseTimeout); err != nil {
		return "", err
	}
	if len(resp.Info.Error) > 0 && string(resp.Info.Error) != "null" {
		return "", openCodeError(resp.Info.Error)
	}
	var out strings.Builder
	for _, part := range resp.Parts {
		if part.Type == "text" {
			out.WriteString(part.Text)
		}
	}
	if strings.TrimSpace(out.String()) == "" {
		return "", &RequestError{Outcome: domain.OutcomeInvalidResponse, Message: "OpenCode returned empty response"}
	}
	return strings.TrimSpace(out.String()), nil
}

func openCodeRuntimeKey(credential, upstreamProxy string) string {
	return credential + "\x00" + upstreamProxy
}

func splitOpenCodeRef(ref, fallbackProvider string) (string, string) {
	ref = strings.TrimSpace(ref)
	if i := strings.Index(ref, "/"); i > 0 {
		return ref[:i], ref[i+1:]
	}
	if fallbackProvider == "" {
		fallbackProvider = "opencode"
	}
	return fallbackProvider, ref
}

func (p *EmbeddedOpenCode) Close() { p.runtimes.close() }

// Keep only ordinary process-launch and TLS environment. In particular do not
// inherit OpenCode configs, credential files, provider secrets or proxy bypasses.
func sanitizedEnv() []string {
	allowed := map[string]bool{"PATH": true, "PATHEXT": true, "SYSTEMROOT": true, "WINDIR": true, "COMSPEC": true, "SYSTEMDRIVE": true, "TEMP": true, "TMP": true, "LANG": true, "LC_ALL": true, "TZ": true, "SSL_CERT_FILE": true, "SSL_CERT_DIR": true}
	out := []string{}
	for _, item := range os.Environ() {
		name, _, _ := strings.Cut(item, "=")
		if allowed[strings.ToUpper(name)] {
			out = append(out, item)
		}
	}
	return out
}

type openCodeServer struct {
	baseURL       string
	port          int
	password      string
	cmd           *exec.Cmd
	providerID    string
	credentialEnv []string
	credential    string
	upstreamProxy string
	tempDir       string
}

func (s *openCodeServer) environment(cfgJSON []byte) []string {
	env := sanitizedEnv()
	env = append(env, "HOME="+s.tempDir, "USERPROFILE="+s.tempDir, "APPDATA="+filepath.Join(s.tempDir, "config"), "LOCALAPPDATA="+filepath.Join(s.tempDir, "data"), "OPENCODE_SERVER_PASSWORD="+s.password, "OPENCODE_CONFIG_CONTENT="+string(cfgJSON), "XDG_CONFIG_HOME="+filepath.Join(s.tempDir, "config"), "XDG_DATA_HOME="+filepath.Join(s.tempDir, "data"), "XDG_CACHE_HOME="+filepath.Join(s.tempDir, "cache"), "XDG_STATE_HOME="+filepath.Join(s.tempDir, "state"), "HTTPS_PROXY="+s.upstreamProxy, "HTTP_PROXY="+s.upstreamProxy, "NO_PROXY=127.0.0.1,localhost")
	if s.credential != "" && len(s.credentialEnv) > 0 {
		env = append(env, s.credentialEnv[0]+"="+s.credential)
	}
	return env
}

func (s *openCodeServer) alive() bool {
	return s.cmd != nil && s.cmd.Process != nil && s.cmd.ProcessState == nil
}
func (s *openCodeServer) start(ctx context.Context) error {
	bin, err := findOpenCodeBinary()
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return err
	}
	s.port = ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	s.password = fmt.Sprintf("%d", time.Now().UnixNano())
	s.tempDir, err = os.MkdirTemp("", "aioverlay-go-opencode-")
	if err != nil {
		return err
	}
	options := map[string]any{}
	if s.credential != "" {
		options["apiKey"] = s.credential
	}
	if s.baseURL != "" {
		options["baseURL"] = s.baseURL
	}
	cfg := map[string]any{"provider": map[string]any{s.providerID: map[string]any{"options": options}}, "enabled_providers": []string{s.providerID}, "tools": map[string]bool{"*": false}, "permission": map[string]string{"*": "deny"}}
	cfgJSON, _ := json.Marshal(cfg)
	env := s.environment(cfgJSON)
	s.cmd = exec.Command(bin, "serve", "--pure", "--hostname", "127.0.0.1", "--port", strconv.Itoa(s.port))
	configureHiddenChild(s.cmd)
	s.cmd.Env = env
	s.cmd.Dir = s.tempDir
	s.cmd.Stdout = io.Discard
	s.cmd.Stderr = io.Discard
	if err := s.cmd.Start(); err != nil {
		return err
	}
	deadline := time.Now().Add(7 * time.Second)
	for time.Now().Before(deadline) {
		var health struct {
			Healthy bool `json:"healthy"`
		}
		if s.doJSON(ctx, http.MethodGet, "/global/health", nil, &health, time.Second) == nil && health.Healthy {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(80 * time.Millisecond):
		}
	}
	s.stop()
	return &RequestError{Outcome: domain.OutcomeProviderUnavailable, Message: "embedded OpenCode did not become healthy", Retryable: true}
}
func (s *openCodeServer) stop() {
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
		_, _ = s.cmd.Process.Wait()
	}
	s.cmd = nil
	if s.tempDir != "" {
		_ = os.RemoveAll(s.tempDir)
	}
}
func (s *openCodeServer) createSession(ctx context.Context) (string, error) {
	var out struct {
		ID string `json:"id"`
	}
	if err := s.doJSON(ctx, http.MethodPost, "/session", map[string]any{"title": "AIOverlay-go"}, &out, 5*time.Second); err != nil {
		return "", err
	}
	if out.ID == "" {
		return "", &RequestError{Outcome: domain.OutcomeInvalidResponse, Message: "OpenCode session id missing"}
	}
	return out.ID, nil
}
func (s *openCodeServer) doJSON(ctx context.Context, method, path string, payload any, out any, timeout time.Duration) error {
	var body io.Reader
	if payload != nil {
		b, _ := json.Marshal(payload)
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://127.0.0.1:"+strconv.Itoa(s.port)+path, body)
	if err != nil {
		return err
	}
	rawAuth := base64.StdEncoding.EncodeToString([]byte("opencode:" + s.password))
	req.Header.Set("Authorization", "Basic "+rawAuth)
	req.Header.Set("Content-Type", "application/json")
	client := http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return &RequestError{Outcome: domain.OutcomeNetworkError, Message: err.Error(), Retryable: true}
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return HTTPError(resp.StatusCode, string(raw))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func findOpenCodeBinary() (string, error) {
	if override := strings.TrimSpace(os.Getenv("AIOVERLAY_OPENCODE_BIN")); override != "" {
		if st, err := os.Stat(override); err == nil && !st.IsDir() {
			return override, nil
		}
	}
	candidates := []string{}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		candidates = append(candidates, filepath.Join(dir, "runtime", "opencode", "opencode.exe"), filepath.Join(dir, "runtime", "opencode", "opencode"))
	}
	candidates = append(candidates, filepath.Join("runtime", "opencode", "opencode.exe"), filepath.Join("runtime", "opencode", "opencode"))
	for _, p := range candidates {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	return "", fmt.Errorf("embedded OpenCode runtime not found")
}
