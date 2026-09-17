package providers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"aioverlay/internal/domain"
)

type OpenAICompatible struct {
	cfg    domain.ProviderConfig
	client *http.Client
	proxy  ProxyResolver
}

func NewOpenAI(cfg domain.ProviderConfig, resolver ...ProxyResolver) *OpenAICompatible {
	var proxy ProxyResolver
	if len(resolver) > 0 {
		proxy = resolver[0]
	}
	return &OpenAICompatible{cfg: cfg, client: &http.Client{}, proxy: proxy}
}
func (p *OpenAICompatible) ID() string { return p.cfg.ID }

func (p *OpenAICompatible) Stream(ctx context.Context, r Request, credential string) (<-chan Event, <-chan error) {
	events := make(chan Event, 16)
	errs := make(chan error, 1)
	go func() {
		defer close(events)
		defer close(errs)
		if err := p.stream(ctx, r, credential, events); err != nil {
			errs <- err
		}
	}()
	return events, errs
}

func (p *OpenAICompatible) stream(ctx context.Context, r Request, credential string, events chan<- Event) error {
	if err := validateAudioRequest(p.cfg, r); err != nil {
		return err
	}
	content := any(r.UserPrompt)
	if len(r.ImageJPEG) > 0 {
		content = []any{
			map[string]any{"type": "text", "text": r.UserPrompt},
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(r.ImageJPEG)}},
		}
	}
	if r.Audio != nil {
		parts, ok := content.([]any)
		if !ok {
			parts = []any{map[string]any{"type": "text", "text": r.UserPrompt}}
		}
		content = append(parts, map[string]any{"type": "input_audio", "input_audio": map[string]any{"data": base64.StdEncoding.EncodeToString(r.Audio.WAV), "format": "wav"}})
	}
	messages := []any{}
	if strings.TrimSpace(r.SystemPrompt) != "" {
		messages = append(messages, map[string]any{"role": "system", "content": r.SystemPrompt})
	}
	messages = append(messages, map[string]any{"role": "user", "content": content})
	payload := map[string]any{"model": r.ModelID, "messages": messages, "stream": true, "max_tokens": r.MaxOutputTokens, "stream_options": map[string]any{"include_usage": true}}
	body, _ := json.Marshal(payload)
	endpoint := strings.TrimRight(p.cfg.BaseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return &RequestError{Outcome: domain.OutcomeValidationFailed, Message: "Invalid provider endpoint URL"}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if credential != "" {
		req.Header.Set("Authorization", "Bearer "+credential)
	}
	for k, v := range p.cfg.Headers {
		req.Header.Set(k, v)
	}
	client := *p.client
	tr, transportErr := requestTransport(ctx, p.proxy, r.ConnectTimeout)
	if transportErr != nil {
		return &RequestError{Outcome: domain.OutcomeNetworkError, Message: transportErr.Error(), Retryable: true}
	}
	client.Transport = tr
	defer tr.CloseIdleConnections()
	if r.ResponseTimeout > 0 {
		client.Timeout = r.ResponseTimeout
	}
	resp, err := client.Do(req)
	if err != nil {
		return networkError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return HTTPError(resp.StatusCode, string(raw))
	}
	if !sendEvent(ctx, events, Event{Type: EventStart, ModelID: r.ModelID}) {
		return ctx.Err()
	}
	first := true
	finished := false
	inputTokens, outputTokens := 0, 0
	resolved := r.ModelID
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		raw := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if raw == "" {
			continue
		}
		if raw == "[DONE]" {
			if !sendEvent(ctx, events, Event{Type: EventDone, ModelID: resolved, InputTokens: inputTokens, OutputTokens: outputTokens}) {
				return ctx.Err()
			}
			return nil
		}
		var item struct {
			Model   string          `json:"model"`
			Error   json.RawMessage `json:"error"`
			Choices []struct {
				Delta struct {
					Content any `json:"content"`
				} `json:"delta"`
				FinishReason any `json:"finish_reason"`
			} `json:"choices"`
			Usage struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
			} `json:"usage"`
		}
		if json.Unmarshal([]byte(raw), &item) != nil {
			continue
		}
		if len(item.Error) > 0 && string(item.Error) != "null" {
			return &RequestError{Outcome: domain.OutcomeProviderUnavailable, Message: "provider returned a streaming error", Retryable: true}
		}
		if item.Model != "" {
			resolved = item.Model
		}
		if item.Usage.PromptTokens > 0 {
			inputTokens = item.Usage.PromptTokens
		}
		if item.Usage.CompletionTokens > 0 {
			outputTokens = item.Usage.CompletionTokens
		}
		if len(item.Choices) == 0 {
			continue
		}

		delta := contentText(item.Choices[0].Delta.Content)
		if delta != "" {
			if first {
				if !sendEvent(ctx, events, Event{Type: EventFirstToken, ModelID: r.ModelID}) {
					return ctx.Err()
				}
				first = false
			}
			if !sendEvent(ctx, events, Event{Type: EventDelta, Text: delta, ModelID: r.ModelID}) {
				return ctx.Err()
			}
		}
		if item.Choices[0].FinishReason != nil {
			finished = true
		}
	}
	if err := scanner.Err(); err != nil {
		return networkError(err)
	}
	if finished {
		if !sendEvent(ctx, events, Event{Type: EventDone, ModelID: resolved, InputTokens: inputTokens, OutputTokens: outputTokens}) {
			return ctx.Err()
		}
		return nil
	}
	return &RequestError{Outcome: "invalid_response", Message: "stream ended without completion"}
}

func contentText(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []any:
		var b strings.Builder
		for _, it := range x {
			if m, ok := it.(map[string]any); ok {
				if s, ok := m["text"].(string); ok {
					b.WriteString(s)
				}
			}
		}
		return b.String()
	default:
		return ""
	}
}

var _ Provider = (*OpenAICompatible)(nil)
var _ = fmt.Sprintf
var _ = time.Second
