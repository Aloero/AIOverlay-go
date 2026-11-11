package providers

import (
	"aioverlay/internal/domain"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"
)

// Transcription is one bounded /audio/transcriptions request. Selection, retries,
// credentials and fallback remain in engine. WAV is never written to disk.
type Transcription struct {
	cfg   domain.ProviderConfig
	proxy ProxyResolver
}

func NewTranscription(c domain.ProviderConfig, p ProxyResolver) *Transcription {
	return &Transcription{c, p}
}

// Capabilities is this adapter's own declaration of what it can carry. It is reported to the
// scheduler and to Studio, so no layer has to infer modality support from the provider's name.
func (p *Transcription) Capabilities() MediaCapabilities { return Media(p.cfg) }

func init() {
	Register(Factory{
		Kind: domain.ProviderTranscription,
		New: func(p domain.ProviderConfig, deps Deps) (Provider, MediaCapabilities, error) {
			adapter := NewTranscription(p, deps.Proxy)
			return adapter, adapter.Capabilities(), nil
		},
	})
}

func (p *Transcription) ID() string        { return p.cfg.ID }
func (p *Transcription) Incremental() bool { return false }
func (p *Transcription) Stream(ctx context.Context, r Request, key string) (<-chan Event, <-chan error) {
	ev := make(chan Event, 4)
	errs := make(chan error, 1)
	go func() {
		defer close(ev)
		defer close(errs)
		if err := p.request(ctx, r, key, ev); err != nil {
			errs <- err
		}
	}()
	return ev, errs
}
func (p *Transcription) request(ctx context.Context, r Request, key string, ev chan<- Event) error {
	if err := validateAudioRequest(p.cfg, r); err != nil {
		return err
	}
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	if err := writer.WriteField("model", r.ModelID); err != nil {
		return err
	}
	if err := writer.WriteField("response_format", "json"); err != nil {
		return err
	}
	// A transcription prompt is vocabulary/context, not a chat system instruction.
	if r.UserPrompt != "" {
		if err := writer.WriteField("prompt", r.UserPrompt); err != nil {
			return err
		}
	}
	wav, err := wavPCM16(r.Audio)
	if err != nil {
		return &RequestError{Outcome: domain.OutcomeValidationFailed, Message: err.Error()}
	}
	part, err := writer.CreateFormFile("file", "speech.wav")
	if err != nil {
		return err
	}
	if _, err = part.Write(wav); err != nil {
		return err
	}
	if err = writer.Close(); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(p.cfg.BaseURL, "/")+"/audio/transcriptions", &buf)
	if err != nil {
		return &RequestError{Outcome: domain.OutcomeValidationFailed, Message: "Invalid transcription endpoint"}
	}
	for k, v := range p.cfg.Headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	tr, err := requestTransport(ctx, p.proxy, r.ConnectTimeout)
	if err != nil {
		return &RequestError{Outcome: domain.OutcomeNetworkError, Message: err.Error()}
	}
	defer tr.CloseIdleConnections()
	timeout := r.ResponseTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	resp, err := (&http.Client{Transport: tr, Timeout: timeout, CheckRedirect: checkProviderRedirect}).Do(req)
	if err != nil {
		return networkError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return HTTPError(resp.StatusCode, string(raw))
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
	if err != nil {
		return networkError(err)
	}
	var data struct {
		Text string `json:"text"`
	}
	if len(raw) > 2<<20 || json.Unmarshal(raw, &data) != nil {
		return &RequestError{Outcome: domain.OutcomeInvalidResponse, Message: "Invalid transcription response"}
	}
	if !sendEvent(ctx, ev, Event{Type: EventDelta, ModelID: r.ModelID, Text: data.Text}) {
		return ctx.Err()
	}
	if !sendEvent(ctx, ev, Event{Type: EventDone, ModelID: r.ModelID}) {
		return ctx.Err()
	}
	return nil
}
