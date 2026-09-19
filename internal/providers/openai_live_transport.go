package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// openAILiveTransportConfig requires an explicit Live primary-WebSocket URL.
// The transport never guesses or rewrites a production endpoint.
type openAILiveTransportConfig struct {
	Endpoint           string
	APIKey             string
	Session            json.RawMessage
	ResponsesDelegated bool
}

func dialOpenAILiveTransport(ctx context.Context, connector realtimeConnector, config openAILiveTransportConfig) (*openAILiveProtocol, error) {
	if strings.TrimSpace(config.APIKey) == "" {
		return nil, fmt.Errorf("OpenAI Live requires an API key")
	}
	if _, err := parseRealtimeEndpoint(config.Endpoint); err != nil {
		return nil, err
	}
	if !validJSONObject(config.Session) {
		return nil, fmt.Errorf("OpenAI Live requires a session JSON object")
	}

	headers := make(http.Header)
	headers.Set("Authorization", "Bearer "+config.APIKey)
	socket, err := dialRealtimeWebSocket(ctx, connector, config.Endpoint, headers)
	if err != nil {
		return nil, err
	}
	protocol := newOpenAILiveProtocol(socket, config.ResponsesDelegated)
	if err := protocol.Start(ctx, config.Session); err != nil {
		_ = protocol.Close()
		return nil, err
	}
	return protocol, nil
}
