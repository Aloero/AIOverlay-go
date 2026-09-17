package providers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"

	"aioverlay/internal/domain"
)

type Gemini struct {
	cfg    domain.ProviderConfig
	client *http.Client
	proxy  ProxyResolver
}

func NewGemini(cfg domain.ProviderConfig, resolver ...ProxyResolver) *Gemini {
	var proxy ProxyResolver
	if len(resolver) > 0 {
		proxy = resolver[0]
	}
	return &Gemini{cfg: cfg, client: &http.Client{}, proxy: proxy}
}
func (p *Gemini) ID() string { return p.cfg.ID }
func (p *Gemini) Stream(ctx context.Context, r Request, key string) (<-chan Event, <-chan error) {
	e := make(chan Event, 16)
	er := make(chan error, 1)
	go func() {
		defer close(e)
		defer close(er)
		if x := p.stream(ctx, r, key, e); x != nil {
			er <- x
		}
	}()
	return e, er
}
func (p *Gemini) stream(ctx context.Context, r Request, key string, events chan<- Event) error {
	if err := validateAudioRequest(p.cfg, r); err != nil {
		return err
	}
	parts := []any{map[string]any{"text": r.UserPrompt}}
	if r.Audio != nil {
		wav, err := wavPCM16(r.Audio)
		if err != nil {
			return &RequestError{Outcome: domain.OutcomeValidationFailed, Message: err.Error()}
		}
		parts = append(parts, map[string]any{"inline_data": map[string]any{"mime_type": "audio/wav", "data": base64.StdEncoding.EncodeToString(wav)}})
	}

	if len(r.ImageJPEG) > 0 {
		parts = append(parts, map[string]any{"inline_data": map[string]any{"mime_type": "image/jpeg", "data": base64.StdEncoding.EncodeToString(r.ImageJPEG)}})
	}
	payload := map[string]any{"contents": []any{map[string]any{"role": "user", "parts": parts}}, "generationConfig": map[string]any{"maxOutputTokens": r.MaxOutputTokens}}
	if strings.TrimSpace(r.SystemPrompt) != "" {
		payload["systemInstruction"] = map[string]any{"parts": []any{map[string]any{"text": r.SystemPrompt}}}
	}
	b, _ := json.Marshal(payload)
	base := strings.TrimRight(p.cfg.BaseURL, "/")
	endpoint := base + "/models/" + url.PathEscape(r.ModelID) + ":streamGenerateContent?alt=sse"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(b))
	if err != nil {
		return &RequestError{Outcome: domain.OutcomeValidationFailed, Message: "Invalid Gemini endpoint URL"}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("x-goog-api-key", key)
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
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		raw := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		var item struct {
			Candidates []struct {
				Content struct {
					Parts []struct {
						Text    string `json:"text"`
						Thought bool   `json:"thought"`
					} `json:"parts"`
				} `json:"content"`
				FinishReason string `json:"finishReason"`
			} `json:"candidates"`
			UsageMetadata struct {
				PromptTokenCount     int `json:"promptTokenCount"`
				CandidatesTokenCount int `json:"candidatesTokenCount"`
			} `json:"usageMetadata"`
		}
		if json.Unmarshal([]byte(raw), &item) != nil || len(item.Candidates) == 0 {
			continue
		}
		var d strings.Builder
		for _, p := range item.Candidates[0].Content.Parts {
			if !p.Thought {
				d.WriteString(p.Text)
			}
		}
		if d.Len() > 0 {
			if first {
				if !sendEvent(ctx, events, Event{Type: EventFirstToken, ModelID: r.ModelID}) {
					return ctx.Err()
				}
				first = false
			}
			if !sendEvent(ctx, events, Event{Type: EventDelta, Text: d.String(), ModelID: r.ModelID}) {
				return ctx.Err()
			}
		}
		if item.Candidates[0].FinishReason != "" {
			if !sendEvent(ctx, events, Event{Type: EventDone, ModelID: r.ModelID, InputTokens: item.UsageMetadata.PromptTokenCount, OutputTokens: item.UsageMetadata.CandidatesTokenCount}) {
				return ctx.Err()
			}
			return nil
		}
	}
	if sc.Err() != nil {
		return networkError(sc.Err())
	}
	return &RequestError{Outcome: "invalid_response", Message: "stream ended without completion"}
}
