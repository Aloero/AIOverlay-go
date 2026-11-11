// Hermes transport. The harness is driven as a one-shot CLI call, fully explicit and isolated:
// --safe-mode never reads the user's profile, --provider custom carries exactly the endpoint and
// key this app already owns, and --format stream-json replaces scraping terminal output. The app
// deliberately delegates model parameters (reasoning effort and anything the harness adds later)
// to the harness: parameters are passed as flags the harness defines, not re-implemented here.
// The harness is the connector to arbitrary models; the screenshot is passed as the CLI's
// documented `--image <path>` attachment as soon as the transport declares image support (the
// gateway authority of the current harness build rejects that flag for headless runs, so the
// policy keeps the route text-only until the front can carry pixels).
package providers

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"aioverlay/internal/domain"
	"aioverlay/internal/modelhost"
	hermeshost "aioverlay/internal/modelhost/hermes"
)

type EmbeddedHermes struct {
	cfg   domain.ProviderConfig
	proxy ProxyResolver
	host  hermesRuntime
}

// hermesRuntime is the part of the model host this adapter needs. The CLI transport never talks
// to an HTTP endpoint, but executable lookup, version reporting and the isolated HERMES_HOME
// still belong to the host, not to the request layer.
type hermesRuntime interface {
	modelhost.Host
	Executable() (string, error)
	Home() (string, error)
}

func NewEmbeddedHermes(cfg domain.ProviderConfig, resolver ...ProxyResolver) *EmbeddedHermes {
	var proxy ProxyResolver
	if len(resolver) > 0 {
		proxy = resolver[0]
	}
	return newEmbeddedHermes(cfg, Deps{Proxy: proxy})
}

func init() {
	Register(Factory{
		Kind: domain.ProviderEmbeddedHermes,
		New: func(cfg domain.ProviderConfig, deps Deps) (Provider, MediaCapabilities, error) {
			adapter := newEmbeddedHermes(cfg, deps)
			return adapter, adapter.Capabilities(), nil
		},
	})
}

func newEmbeddedHermes(cfg domain.ProviderConfig, deps Deps) *EmbeddedHermes {
	adapter := &EmbeddedHermes{cfg: cfg, proxy: deps.Proxy}
	host := deps.host(cfg, func() modelhost.Host { return hermeshost.New(hermeshost.Config{}) })
	if runtime, ok := host.(hermesRuntime); ok {
		adapter.host = runtime
	}
	return adapter
}

func (p *EmbeddedHermes) ID() string { return p.cfg.ID }

func (p *EmbeddedHermes) Capabilities() MediaCapabilities { return Media(p.cfg) }

// Incremental reports one body per attempt, like the OpenCode transport: the JSONL text events
// are accumulated before delivery so the scheduler sees the same contract as everywhere else.
func (p *EmbeddedHermes) Incremental() bool { return false }

func (p *EmbeddedHermes) Host() modelhost.Host {
	if p.host == nil {
		return nil
	}
	return p.host
}

func (p *EmbeddedHermes) Stream(ctx context.Context, r Request, credential string) (<-chan Event, <-chan error) {
	events := make(chan Event, 4)
	errs := make(chan error, 1)
	go func() {
		defer close(events)
		defer close(errs)
		events <- Event{Type: EventStart, ModelID: r.ModelID}
		text, tokens, err := p.run(ctx, r, credential)
		if err != nil {
			errs <- err
			return
		}
		events <- Event{Type: EventFirstToken, ModelID: r.ModelID}
		events <- Event{Type: EventDelta, Text: text, ModelID: r.ModelID}
		events <- Event{Type: EventDone, ModelID: r.ModelID, InputTokens: tokens.input, OutputTokens: tokens.output}
	}()
	return events, errs
}

type hermesTokens struct{ input, output int }

func (p *EmbeddedHermes) run(ctx context.Context, r Request, credential string) (string, hermesTokens, error) {
	if p.host == nil {
		return "", hermesTokens{}, &RequestError{Outcome: domain.OutcomeValidationFailed, Message: "Hermes connection needs a CLI-capable model host"}
	}
	modelID, err := hermesModelID(r.ModelID, p.cfg.GatewayProviderID())
	if err != nil {
		return "", hermesTokens{}, err
	}
	if strings.TrimSpace(p.cfg.BaseURL) == "" {
		return "", hermesTokens{}, &RequestError{Outcome: domain.OutcomeValidationFailed, Message: "Hermes connection has no upstream base URL"}
	}
	egress, err := p.egress(ctx)
	if err != nil {
		return "", hermesTokens{}, err
	}
	path, err := p.host.Executable()
	if err != nil {
		return "", hermesTokens{}, hostFailure(err)
	}
	home, err := p.host.Home()
	if err != nil {
		return "", hermesTokens{}, hostFailure(err)
	}
	args, err := hermesArgs(p.cfg, credential, modelID, r.AppParams)
	if err != nil {
		return "", hermesTokens{}, err
	}
	// The CLI takes one local image per query as a file path, so the screenshot is materialized
	// inside the isolated home for the duration of the call and removed right after it. Only
	// transports whose media policy carries images reach this with bytes attached; the flag is
	// sent conditionally because the gateway authority rejects `--image` for headless one-shot
	// runs on the current harness build, and a route must never fail on an option it declared.
	if len(r.ImageJPEG) > 0 && p.Capabilities().Image {
		imageArg, err := writeHermesImage(home, r.ImageJPEG)
		if err != nil {
			return "", hermesTokens{}, err
		}
		defer os.Remove(imageArg)
		args = append(args, "--image", imageArg)
	}
	timeout := r.ResponseTimeout
	if timeout <= 0 {
		timeout = modelhost.StartupTimeout
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := cliCommand(callCtx, path, args...)
	configureHiddenChild(cmd)
	startGroup(cmd)
	cmd.WaitDelay = 2 * time.Second
	cmd.Dir = home
	cmd.Env = hermesEnv(home, egress)

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Stdin = strings.NewReader(hermesPrompt(r))
	runErr := cmd.Run()

	if ctx.Err() != nil {
		return "", hermesTokens{}, &RequestError{Outcome: domain.OutcomeCancelled, Message: "Request cancelled"}
	}
	events, decodeErr := parseHermesStream(stdout.String())
	if decodeErr != nil && events.result == nil {
		// No terminal record: report the harness' own stderr rather than pretending success.
		return "", hermesTokens{}, hermesFailure(runErr, "", stderr.String())
	}
	if runErr != nil || (events.result != nil && (events.result.ExitCode != 0 || events.result.Error != "")) {
		message := ""
		if events.result != nil {
			message = events.result.Error
		}
		if errors.Is(runErr, context.DeadlineExceeded) || callCtx.Err() == context.DeadlineExceeded {
			return "", hermesTokens{}, &RequestError{Outcome: domain.OutcomeTimeout, Message: fmt.Sprintf("Hermes did not answer within %s", timeout), Retryable: true}
		}
		return "", hermesTokens{}, hermesFailure(runErr, message, stderr.String())
	}
	if decodeErr != nil {
		return "", hermesTokens{}, &RequestError{Outcome: domain.OutcomeInvalidResponse, Message: "Hermes returned a broken answer"}
	}
	text := events.text()
	if strings.TrimSpace(text) == "" {
		return "", hermesTokens{}, &RequestError{Outcome: domain.OutcomeInvalidResponse, Message: "Hermes returned empty response"}
	}
	tokens := hermesTokens{}
	if events.result != nil {
		tokens.input, tokens.output = events.result.Tokens.Input, events.result.Tokens.Output
	}
	return text, tokens, nil
}

// egress mirrors the OpenCode transport: the configured AI-VPN when enabled, otherwise the
// IPv4-forcing loopback proxy, because this network shape is where AAAA-only answers stall.
func (p *EmbeddedHermes) egress(ctx context.Context) (string, error) {
	if p.proxy != nil {
		upstream, err := p.proxy.ProxyURL(ctx)
		if err != nil {
			return "", &RequestError{Outcome: domain.OutcomeNetworkError, Message: "AI VPN unavailable: " + err.Error(), Retryable: true}
		}
		if upstream != "" {
			return upstream, nil
		}
	}
	return ipv4ProxyURL()
}

// hermesPrompt composes the one-shot query. The CLI has no per-call system-prompt flag, so the
// rendered prompt travels as the query itself; --query-file - keeps everything verbatim.
func hermesPrompt(r Request) string {
	if strings.TrimSpace(r.SystemPrompt) == "" {
		return r.UserPrompt
	}
	return r.SystemPrompt + "\n\n" + r.UserPrompt
}

// writeHermesImage materializes the screenshot inside the isolated home so the CLI can attach it
// by path, which is the only attachment mode `hermes chat` documents for a single query.
func writeHermesImage(home string, image []byte) (string, error) {
	fail := &RequestError{Outcome: domain.OutcomeValidationFailed, Message: "Hermes cannot attach the screenshot"}
	file, err := os.CreateTemp(home, "aioverlay-image-*.jpg")
	if err != nil {
		return "", fail
	}
	path := file.Name()
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	if _, err = file.Write(image); err != nil {
		file.Close()
		os.Remove(path)
		return "", fail
	}
	if err = file.Close(); err != nil {
		os.Remove(path)
		return "", fail
	}
	return path, nil
}

// hermesModelID accepts "provider/model" and a bare model id like the OpenCode transport, but the
// prefix must name this connection's upstream: a reference from another provider would silently
// be sent to the wrong endpoint.
func hermesModelID(ref, providerID string) (string, error) {
	ref = strings.TrimSpace(ref)
	prefix, model := ref, ref
	if i := strings.Index(ref, "/"); i > 0 {
		prefix, model = ref[:i], ref[i+1:]
		if providerID != "" && prefix != providerID {
			return "", &RequestError{Outcome: domain.OutcomeValidationFailed, Message: "Model provider does not match the configured Hermes connection"}
		}
	}
	if model == "" {
		return "", &RequestError{Outcome: domain.OutcomeValidationFailed, Message: "Model id is empty"}
	}
	return model, nil
}

// hermesArgs builds the one-shot command line. App parameters are forwarded only as flags the
// harness itself defines; an unknown key is the harness' problem to ignore, never a reason this
// app guesses a meaning.
func hermesArgs(cfg domain.ProviderConfig, credential, modelID string, appParams map[string]json.RawMessage) ([]string, error) {
	args := []string{"chat", "--safe-mode", "--provider", "custom", "--base-url", strings.TrimSpace(cfg.BaseURL)}
	if credential != "" {
		args = append(args, "--api-key", credential)
	}
	args = append(args, "--model", modelID, "--query-file", "-", "--format", "stream-json", "--source", "tool")
	if raw, ok := appParams["reasoning"]; ok {
		var effort string
		if err := json.Unmarshal(raw, &effort); err == nil && effort != "" {
			if !safeFlagValue(effort) {
				return nil, &RequestError{Outcome: domain.OutcomeValidationFailed, Message: "reasoning parameter must be a short word"}
			}
			args = append(args, "--reasoning", effort)
		}
	}
	return args, nil
}

func safeFlagValue(v string) bool {
	if v == "" || len(v) > 24 {
		return false
	}
	return strings.Trim(v, "abcdefghijklmnopqrstuvwxyz0123456789-_") == ""
}

// hermesEnv strips every ambient HERMES_* and proxy variable so the harness sees exactly what
// this connection declared, then adds the isolated home and the egress.
func hermesEnv(home, proxy string) []string {
	env := make([]string, 0, len(os.Environ())+5)
	for _, kv := range os.Environ() {
		upper := strings.ToUpper(kv)
		if strings.HasPrefix(upper, "HERMES_") || strings.HasPrefix(upper, "HTTP_PROXY=") || strings.HasPrefix(upper, "HTTPS_PROXY=") || strings.HasPrefix(upper, "ALL_PROXY=") {
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "HERMES_HOME="+home)
	if proxy != "" {
		env = append(env, "HTTP_PROXY="+proxy, "HTTPS_PROXY="+proxy, "ALL_PROXY="+proxy)
	}
	return env
}

// cliCommand runs .cmd/.bat shims through cmd.exe; CreateProcess cannot launch them directly.
func cliCommand(ctx context.Context, path string, args ...string) *exec.Cmd {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".cmd", ".bat":
		head := append([]string{"/d", "/c", path}, args...)
		return exec.CommandContext(ctx, "cmd.exe", head...)
	default:
		return exec.CommandContext(ctx, path, args...)
	}
}

type hermesResult struct {
	SessionID string `json:"session_id"`
	ExitCode  int    `json:"exit_code"`
	Text      string `json:"text"`
	Error     string `json:"error"`
	Tokens    struct {
		Input  int `json:"input"`
		Output int `json:"output"`
	} `json:"tokens"`
}

type hermesEvents struct {
	deltas       []string
	result       *hermesResult
	sawTextDelta bool
}

func (e *hermesEvents) text() string {
	if e.result != nil && e.result.Text != "" {
		return e.result.Text
	}
	return strings.Join(e.deltas, "")
}

// parseHermesStream reads the stream-json records. Non-JSON lines are ignored: the CLI is told
// to keep diagnostics on stderr, and a stray line must not erase an otherwise complete answer.
func parseHermesStream(out string) (hermesEvents, error) {
	var events hermesEvents
	scanner := bufio.NewScanner(strings.NewReader(out))
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || line[0] != '{' {
			continue
		}
		var record struct {
			Type  string `json:"type"`
			Text  string `json:"text"`
			Error string `json:"error"`
		}
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			continue
		}
		switch record.Type {
		case "text":
			events.deltas = append(events.deltas, record.Text)
			events.sawTextDelta = true
		case "result":
			var result hermesResult
			if err := json.Unmarshal([]byte(line), &result); err != nil {
				return events, err
			}
			events.result = &result
		}
	}
	if events.result == nil && !events.sawTextDelta {
		return events, errors.New("no stream-json records")
	}
	return events, nil
}

// hermesFailure maps a failed one-shot run onto an outcome. The harness keeps provider errors in
// the result record and in stderr; both are quoted bounded and never contain the key, which was
// a launch argument the harness itself holds in memory.
func hermesFailure(runErr error, resultError, stderr string) *RequestError {
	detail := strings.TrimSpace(resultError)
	if detail == "" {
		tail := strings.TrimSpace(stderr)
		if len(tail) > 400 {
			tail = tail[len(tail)-400:]
		}
		detail = tail
	}
	if detail == "" && runErr != nil {
		detail = runErr.Error()
	}
	lower := strings.ToLower(detail)
	outcome, retryable := domain.OutcomeProviderUnavailable, true
	switch {
	case containsAny(lower, "model_not_found", "model not found", "unknown model", "no such model"):
		outcome, retryable = domain.OutcomeModelUnavailable, false
	case containsAny(lower, "429", "rate limit", "too many requests"):
		outcome, retryable = domain.OutcomeRateLimited, true
	case containsAny(lower, "quota", "billing", "insufficient", "credits"):
		outcome, retryable = domain.OutcomeQuotaExceeded, false
	case containsAny(lower, "401", "403", "unauthorized", "invalid api key", "authentication", "forbidden"):
		outcome, retryable = domain.OutcomeAuthFailed, false
	case containsAny(lower, "timeout", "timed out", "deadline"):
		outcome, retryable = domain.OutcomeTimeout, true
	case containsAny(lower, "connection", "dns", "unreachable", "network", "econn"):
		outcome, retryable = domain.OutcomeNetworkError, true
	}
	return &RequestError{Outcome: outcome, Message: "Hermes call failed", Detail: bound(detail), Retryable: retryable}
}

func containsAny(haystack string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(haystack, needle) {
			return true
		}
	}
	return false
}

func bound(s string) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > 200 {
		return string(r[:200])
	}
	return s
}
