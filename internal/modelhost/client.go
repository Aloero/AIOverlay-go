package modelhost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"aioverlay/internal/netpolicy"
)

// ErrInvalidResponse marks a response that arrived but could not be used (oversized, or not the
// JSON object contract). It is separate from a transport error because the outcomes differ: one is
// "the host is broken", the other is "the host is unreachable".
var ErrInvalidResponse = errors.New("model host returned an unusable response")

// MaxResponseBytes bounds one decoded host response. A runaway or hostile runtime must not be
// able to allocate without limit inside the overlay process.
const MaxResponseBytes = 16 << 20

// DoJSON sends one JSON request to the endpoint and returns the status, the raw body and a
// transport error. It deliberately does not classify failures into domain outcomes: what a 402
// or a 503 *means* belongs to the protocol adapter, while this method only guarantees the plumbing
// is the same for every adapter - authentication, the shared redirect policy, the egress proxy and
// a hard response size cap.
func (e Endpoint) DoJSON(ctx context.Context, method, path string, payload, out any, timeout time.Duration) (int, []byte, error) {
	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return 0, nil, err
		}
		body = bytes.NewReader(raw)
	}
	endpoint := strings.TrimRight(e.BaseURL, "/") + path
	if ctx == nil {
		ctx = context.Background()
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if auth := e.Authorization(); auth != "" {
		req.Header.Set("Authorization", "Basic "+auth)
	}
	for name, value := range e.Headers {
		req.Header.Set(name, value)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if e.Proxy != "" {
		parsed, err := url.Parse(e.Proxy)
		if err != nil {
			return 0, nil, fmt.Errorf("model host proxy URL is invalid")
		}
		transport.Proxy = http.ProxyURL(parsed)
	}
	client := &http.Client{
		Timeout:   timeout,
		Transport: transport,
		// Never follow a redirect off the origin the host gave us: the request carries its password.
		CheckRedirect: netpolicy.SameOriginRedirect,
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBytes+1))
	if err != nil {
		return resp.StatusCode, nil, err
	}
	if len(raw) > MaxResponseBytes {
		return resp.StatusCode, nil, fmt.Errorf("%w: response exceeds %d bytes", ErrInvalidResponse, MaxResponseBytes)
	}
	if resp.StatusCode/100 != 2 {
		return resp.StatusCode, raw, nil
	}
	if out != nil && len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return resp.StatusCode, raw, fmt.Errorf("%w: invalid JSON: %v", ErrInvalidResponse, err)
		}
	}
	return resp.StatusCode, raw, nil
}
