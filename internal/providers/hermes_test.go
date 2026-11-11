package providers

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"aioverlay/internal/domain"
	"aioverlay/internal/modelhost"
)

// fakeHermes is a CLI host pointed at a script, so the adapter is exercised over a real
// process and a real stream-json transcript without requiring an installed harness.
type fakeHermes struct {
	path string
	err  error
}

func (f *fakeHermes) ID() string                  { return "hermes" }
func (f *fakeHermes) Version() string             { return "fake" }
func (f *fakeHermes) Executable() (string, error) { return f.path, f.err }
func (f *fakeHermes) Home() (string, error)       { return os.MkdirTemp("", "fake-hermes-") }
func (f *fakeHermes) Endpoint(context.Context, modelhost.Need) (modelhost.Endpoint, func(), error) {
	return modelhost.Endpoint{Host: "hermes", Version: "fake"}, func() {}, f.err
}
func (f *fakeHermes) Served(context.Context, modelhost.Need) ([]modelhost.ModelRef, error) {
	return nil, modelhost.ErrServedUnsupported
}
func (f *fakeHermes) Close() {}

func hermesFixture(t *testing.T, script string) (*EmbeddedHermes, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell fixture")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "hermes")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "args")
	t.Setenv("AIO_TEST_ARGS", logPath)
	adapter := newEmbeddedHermes(domain.ProviderConfig{
		ID: "hermes-test", Kind: domain.ProviderEmbeddedHermes, App: domain.AppHermes,
		BaseURL: "https://upstream.test/v1", OpenCodeProviderID: "openrouter",
	}, Deps{HostFactory: func(domain.ProviderConfig) (modelhost.Host, error) {
		return &fakeHermes{path: path}, nil
	}})
	return adapter, logPath
}

func readArgs(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func collect(t *testing.T, adapter *EmbeddedHermes, req Request) (string, Event, error) {
	t.Helper()
	events, errs := adapter.Stream(context.Background(), req, "sk-test")
	text := ""
	var done Event
	for ev := range events {
		if ev.Type == EventDelta {
			text += ev.Text
		}
		if ev.Type == EventDone {
			done = ev
		}
	}
	var runErr error
	for err := range errs {
		runErr = err
	}
	return text, done, runErr
}

func TestHermesSuccessStream(t *testing.T) {
	adapter, argsPath := hermesFixture(t, `printf '%s\n' "$@" > "$AIO_TEST_ARGS"
cat > /dev/null
printf '{"type":"system","subtype":"init","model":"m","session_id":"s"}\n'
printf '{"type":"text","text":"he"}\n'
printf '{"type":"text","text":"llo"}\n'
printf '{"type":"result","session_id":"s","exit_code":0,"text":"hello","tokens":{"input":3,"output":4,"total":7}}\n'`)
	text, done, err := collect(t, adapter, Request{
		ModelID: "openrouter/anthropic/model-x", UserPrompt: "hi", SystemPrompt: "be brief",
		AppParams: map[string]json.RawMessage{"reasoning": json.RawMessage(`"high"`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if text != "hello" {
		t.Fatalf("text %q", text)
	}
	if done.InputTokens != 3 || done.OutputTokens != 4 {
		t.Fatalf("tokens %+v", done)
	}
	args := readArgs(t, argsPath)
	for _, want := range []string{"--safe-mode", "--provider", "custom", "--base-url", "https://upstream.test/v1", "--api-key", "sk-test", "--model", "anthropic/model-x", "--format", "stream-json", "--reasoning", "high"} {
		if !strings.Contains(args, want) {
			t.Fatalf("args missing %q:\n%s", want, args)
		}
	}
}

func TestHermesImageAttachmentRemoved(t *testing.T) {
	adapter, argsPath := hermesFixture(t, `printf '%s\n' "$@" > "$AIO_TEST_ARGS"
prev=""
for a in "$@"; do
	if [ "$prev" = "--image" ]; then cp "$a" "$AIO_TEST_IMAGE"; fi
	prev="$a"
done
cat > /dev/null
printf '{"type":"system","subtype":"init","model":"m","session_id":"s"}\n'
printf '{"type":"result","session_id":"s","exit_code":0,"text":"ok","tokens":{"input":1,"output":2,"total":3}}\n'`)
	imagePath := filepath.Join(t.TempDir(), "received.jpg")
	t.Setenv("AIO_TEST_IMAGE", imagePath)
	if !adapter.Capabilities().Image {
		t.Fatal("hermes transport does not accept images")
	}
	_, _, err := collect(t, adapter, Request{ModelID: "openrouter/m", UserPrompt: "what is on screen", ImageJPEG: []byte("fake-jpeg-bytes")})
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(imagePath)
	if err != nil {
		t.Fatalf("CLI never saw the attachment: %v", err)
	}
	if string(body) != "fake-jpeg-bytes" {
		t.Fatalf("attachment bytes %q", body)
	}
	// The path in argv is the temp file itself; after the call the adapter must have removed it.
	lines := strings.Split(readArgs(t, argsPath), "\n")
	for i, line := range lines {
		if line != "--image" {
			continue
		}
		if i+1 >= len(lines) {
			t.Fatal("--image without a path")
		}
		if _, err := os.Stat(lines[i+1]); !os.IsNotExist(err) {
			t.Fatalf("screenshot temp file survived the call: %s", lines[i+1])
		}
		return
	}
	t.Fatalf("args missing --image:\n%s", strings.Join(lines, " "))
}

func TestHermesTextOnlyRequestHasNoImageFlag(t *testing.T) {
	adapter, argsPath := hermesFixture(t, `printf '%s\n' "$@" > "$AIO_TEST_ARGS"
cat > /dev/null
printf '{"type":"system","subtype":"init","model":"m","session_id":"s"}\n'
printf '{"type":"result","session_id":"s","exit_code":0,"text":"ok","tokens":{"input":1,"output":2,"total":3}}\n'`)
	if _, _, err := collect(t, adapter, Request{ModelID: "openrouter/m", UserPrompt: "hi"}); err != nil {
		t.Fatal(err)
	}
	if args := readArgs(t, argsPath); strings.Contains(args, "--image") {
		t.Fatalf("text-only call attached an image:\n%s", args)
	}
}

func TestHermesResultErrorMapping(t *testing.T) {
	adapter, _ := hermesFixture(t, `cat > /dev/null
printf '{"type":"result","session_id":"s","exit_code":1,"error":"HTTP 429: rate limit exceeded"}\n'
exit 1`)
	_, _, err := collect(t, adapter, Request{ModelID: "openrouter/m", UserPrompt: "hi", ResponseTimeout: 10 * time.Second})
	var requestError *RequestError
	if !asRequestError(err, &requestError) {
		t.Fatalf("not a RequestError: %v", err)
	}
	if requestError.Outcome != domain.OutcomeRateLimited || !requestError.Retryable {
		t.Fatalf("mapping %+v", requestError)
	}
	if !strings.Contains(requestError.Detail, "429") {
		t.Fatalf("detail lost: %q", requestError.Detail)
	}
}

func TestHermesProviderMismatch(t *testing.T) {
	adapter, _ := hermesFixture(t, `printf '{"type":"result","exit_code":0,"text":"x"}\n'`)
	_, _, err := collect(t, adapter, Request{ModelID: "otherprovider/m", UserPrompt: "hi"})
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("mismatch accepted: %v", err)
	}
}

func TestHermesEmptyAnswer(t *testing.T) {
	adapter, _ := hermesFixture(t, `cat > /dev/null
printf '{"type":"result","session_id":"s","exit_code":0,"text":""}\n'`)
	_, _, err := collect(t, adapter, Request{ModelID: "openrouter/m", UserPrompt: "hi"})
	var requestError *RequestError
	if !asRequestError(err, &requestError) || requestError.Outcome != domain.OutcomeInvalidResponse {
		t.Fatalf("want invalid_response, got %v", err)
	}
}

func TestHermesCancellation(t *testing.T) {
	adapter, _ := hermesFixture(t, `cat > /dev/null
sleep 5
printf '{"type":"result","exit_code":0,"text":"late"}\n'`)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()
	events, errs := adapter.Stream(ctx, Request{ModelID: "openrouter/m", UserPrompt: "hi"}, "sk")
	for range events {
	}
	var runErr error
	for err := range errs {
		runErr = err
	}
	var requestError *RequestError
	if !asRequestError(runErr, &requestError) || requestError.Outcome != domain.OutcomeCancelled {
		t.Fatalf("want cancelled, got %v", runErr)
	}
}

func asRequestError(err error, target **RequestError) bool {
	re, ok := err.(*RequestError)
	if ok {
		*target = re
	}
	return ok
}
