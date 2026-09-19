package providers

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"aioverlay/internal/domain"
)

func TestOpenAILiveAdapterSessionLifecycleAndCapabilities(t *testing.T) {
	serverDone := make(chan error, 1)
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/live/sessions" {
			serverDone <- fmt.Errorf("unexpected path %s", request.URL.Path)
			return
		}
		if request.Header.Get("Authorization") != "Bearer TEST_ONLY_LIVE_KEY" {
			serverDone <- fmt.Errorf("authorization header missing")
			return
		}
		hijacker, ok := writer.(http.Hijacker)
		if !ok {
			serverDone <- fmt.Errorf("server cannot hijack")
			return
		}
		connection, rw, err := hijacker.Hijack()
		if err != nil {
			serverDone <- err
			return
		}
		defer connection.Close()
		if _, err = fmt.Fprintf(rw, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", websocketAccept(request.Header.Get("Sec-WebSocket-Key"))); err == nil {
			err = rw.Flush()
		}
		if err != nil {
			serverDone <- err
			return
		}

		start, err := readLiveClientEvent(rw.Reader)
		if err != nil {
			serverDone <- err
			return
		}
		if start.Type != "session.start" {
			serverDone <- fmt.Errorf("first event=%s", start.Type)
			return
		}
		var sessionConfig struct {
			Model string `json:"model"`
			Audio struct {
				Format struct {
					Type string `json:"type"`
					Rate int    `json:"rate"`
				} `json:"format"`
			} `json:"audio"`
			Delegation struct {
				Type      string `json:"type"`
				Responses struct {
					Model           string `json:"model"`
					MaxOutputTokens int    `json:"max_output_tokens"`
				} `json:"responses"`
			} `json:"delegation"`
		}
		if err := json.Unmarshal(start.Session, &sessionConfig); err != nil {
			serverDone <- err
			return
		}
		if sessionConfig.Model != "gpt-live-1" || sessionConfig.Audio.Format.Type != "audio/pcm" || sessionConfig.Audio.Format.Rate != openAILiveSampleRate || sessionConfig.Delegation.Type != "responses" || sessionConfig.Delegation.Responses.Model != "gpt-5.6-luna" || sessionConfig.Delegation.Responses.MaxOutputTokens != 321 {
			serverDone <- fmt.Errorf("unexpected session config %+v", sessionConfig)
			return
		}
		if err := writeServerFrame(rw.Writer, 0x1, []byte(`{"type":"session.started","client_event_id":"`+start.EventID+`"}`)); err == nil {
			err = rw.Flush()
		}
		if err != nil {
			serverDone <- err
			return
		}

		audio, err := readLiveClientEvent(rw.Reader)
		if err != nil {
			serverDone <- err
			return
		}
		if audio.Type != "session.input_audio.append" {
			serverDone <- fmt.Errorf("audio event=%s", audio.Type)
			return
		}
		var encoded string
		if err := json.Unmarshal(audio.Audio, &encoded); err != nil {
			serverDone <- err
			return
		}
		raw, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil || len(raw) != 240*2 {
			serverDone <- fmt.Errorf("resampled audio bytes=%d err=%v", len(raw), err)
			return
		}

		response, err := readLiveClientEvent(rw.Reader)
		if err != nil || response.Type != "response.create" {
			serverDone <- fmt.Errorf("response event=%+v err=%v", response, err)
			return
		}
		for _, payload := range []string{
			`{"type":"response.event","event":{"type":"response.output_text.delta","delta":"hello"}}`,
			`{"type":"response.event","event":{"type":"response.completed"}}`,
		} {
			if err := writeServerFrame(rw.Writer, 0x1, []byte(payload)); err == nil {
				err = rw.Flush()
			}
			if err != nil {
				serverDone <- err
				return
			}
		}

		closeEvent, err := readLiveClientEvent(rw.Reader)
		if err != nil || closeEvent.Type != "session.close" {
			serverDone <- fmt.Errorf("close event=%+v err=%v", closeEvent, err)
			return
		}
		if err := writeServerFrame(rw.Writer, 0x1, []byte(`{"type":"session.closed"}`)); err == nil {
			err = rw.Flush()
		}
		if err != nil {
			serverDone <- err
			return
		}
		frame, err := readMaskedClientFrame(rw.Reader)
		if err != nil || frame.opcode != 0x8 {
			serverDone <- fmt.Errorf("websocket close frame=%+v err=%v", frame, err)
			return
		}
		serverDone <- nil
	}))
	defer server.Close()

	provider := NewOpenAILive(domain.ProviderConfig{
		ID: "live", Kind: domain.ProviderOpenAILive, Enabled: true,
		BaseURL:         strings.Replace(server.URL, "https://", "wss://", 1) + "/v1/live/sessions",
		RequestDefaults: domain.RequestDefaults{ConnectTimeoutMS: 1000, MaxOutputTokens: 321},
		Realtime:        &domain.RealtimeProviderConfig{ResponsesModel: "gpt-5.6-luna", Instructions: "Listen quietly."},
	}, nil)
	provider.connector.tlsConfig = &tls.Config{InsecureSkipVerify: true} // Local test certificate only.
	caps := provider.RealtimeCapabilities()
	if !caps.Supports(RealtimeAppendAudio) || !caps.Supports(RealtimeRequestResponse) || !caps.Supports(RealtimeTextOutput) || !caps.Supports(RealtimeVPNFailClosed) {
		t.Fatalf("missing supported Live capabilities: %+v", caps)
	}
	for _, unsupported := range []RealtimeOperation{RealtimeUpdateImage, RealtimeSemanticNoAction, RealtimeInterrupt, RealtimeCleanSession, RealtimeReconnect} {
		if caps.Supports(unsupported) {
			t.Fatalf("unsupported Live capability advertised: %s", unsupported)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	realtime, err := provider.OpenRealtime(ctx, RealtimeOpenRequest{ProviderID: "live", ModelID: "gpt-live-1", Credential: "TEST_ONLY_LIVE_KEY"})
	if err != nil {
		t.Fatal(err)
	}
	appender := realtime.(RealtimeAudioAppender)
	requester := realtime.(RealtimeResponseRequester)
	samples := make([]int16, 160)
	for index := range samples {
		samples[index] = int16(index * 10)
	}
	if err := appender.AppendAudio(ctx, &domain.AudioClip{PCM16: samples, SampleRate: 16000, Channels: 1, DurationMS: 10}); err != nil {
		t.Fatal(err)
	}
	events, errors := requester.RequestResponse(ctx)
	var text string
	for event := range events {
		text += event.Text
	}
	if err := <-errors; err != nil {
		t.Fatal(err)
	}
	if text != "hello" {
		t.Fatalf("delegated text=%q", text)
	}
	if err := realtime.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Live test server did not observe clean close")
	}
}

func TestOpenAILiveWithoutResponsesDelegationDoesNotAdvertiseResponse(t *testing.T) {
	provider := NewOpenAILive(domain.ProviderConfig{ID: "live", Kind: domain.ProviderOpenAILive, BaseURL: "wss://api.openai.com/v1/live/sessions"}, nil)
	caps := provider.RealtimeCapabilities()
	if !caps.Supports(RealtimeAppendAudio) || caps.Supports(RealtimeRequestResponse) || caps.Supports(RealtimeTextOutput) {
		t.Fatalf("capabilities=%+v", caps)
	}
}

func readLiveClientEvent(reader *bufio.Reader) (openAILiveEnvelope, error) {
	frame, err := readMaskedClientFrame(reader)
	if err != nil {
		return openAILiveEnvelope{}, err
	}
	if frame.opcode != 0x1 {
		return openAILiveEnvelope{}, fmt.Errorf("client opcode=0x%x", frame.opcode)
	}
	var event openAILiveEnvelope
	if err := json.Unmarshal(frame.payload, &event); err != nil {
		return openAILiveEnvelope{}, err
	}
	return event, nil
}

func TestOpenAILiveStoredSessionReconnectsThroughWebSocketFork(t *testing.T) {
	var primarySeen, forkSeen atomic.Int32
	serverErrors := make(chan error, 2)
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/v1/live/sessions":
			primarySeen.Add(1)
			serveStoredLivePrimary(writer, request, serverErrors)
		case "/v1/live/sessions/source-session/fork":
			forkSeen.Add(1)
			serveStoredLiveFork(writer, request, serverErrors)
		default:
			serverErrors <- fmt.Errorf("unexpected reconnect path %s", request.URL.Path)
		}
	}))
	defer server.Close()

	provider := NewOpenAILive(domain.ProviderConfig{
		ID: "live", Kind: domain.ProviderOpenAILive, Enabled: true,
		BaseURL:  strings.Replace(server.URL, "https://", "wss://", 1) + "/v1/live/sessions",
		Realtime: &domain.RealtimeProviderConfig{Store: true, ResponsesModel: "gpt-5.6-luna"},
	}, nil)
	provider.connector.tlsConfig = &tls.Config{InsecureSkipVerify: true} // Local test certificate only.
	if !provider.RealtimeCapabilities().Supports(RealtimeReconnect) {
		t.Fatal("stored Live provider did not advertise bounded reconnect")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	realtime, err := provider.OpenRealtime(ctx, RealtimeOpenRequest{ProviderID: "live", ModelID: "gpt-live-1", Credential: "TEST_ONLY_LIVE_KEY"})
	if err != nil {
		t.Fatal(err)
	}
	reconnector := realtime.(RealtimeReconnector)
	if err := reconnector.Reconnect(ctx); err != nil {
		_ = realtime.Close()
		t.Fatal(err)
	}
	if err := realtime.(RealtimeAudioAppender).AppendAudio(ctx, &domain.AudioClip{PCM16: make([]int16, 240), SampleRate: 24000, Channels: 1, DurationMS: 10}); err != nil {
		_ = realtime.Close()
		t.Fatal(err)
	}
	if err := realtime.Close(); err != nil {
		t.Fatal(err)
	}
	if primarySeen.Load() != 1 || forkSeen.Load() != 1 {
		t.Fatalf("primary=%d fork=%d", primarySeen.Load(), forkSeen.Load())
	}
	close(serverErrors)
	for err := range serverErrors {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func serveStoredLivePrimary(writer http.ResponseWriter, request *http.Request, errors chan<- error) {
	connection, rw, err := hijackLiveTestConnection(writer, request)
	if err != nil {
		errors <- err
		return
	}
	defer connection.Close()
	start, err := readLiveClientEvent(rw.Reader)
	if err != nil {
		errors <- err
		return
	}
	var config struct {
		Store bool `json:"store"`
	}
	if json.Unmarshal(start.Session, &config) != nil || !config.Store {
		errors <- fmt.Errorf("primary Live session did not enable explicit storage: %s", start.Session)
		return
	}
	if err := writeServerFrame(rw.Writer, 0x1, []byte(`{"type":"session.started","session":{"id":"source-session"}}`)); err == nil {
		err = rw.Flush()
	}
	if err != nil {
		errors <- err
		return
	}
	frame, err := readMaskedClientFrame(rw.Reader)
	if err != nil || frame.opcode != 0x8 {
		errors <- fmt.Errorf("primary reconnect close frame=%+v err=%v", frame, err)
	}
}

func serveStoredLiveFork(writer http.ResponseWriter, request *http.Request, errors chan<- error) {
	connection, rw, err := hijackLiveTestConnection(writer, request)
	if err != nil {
		errors <- err
		return
	}
	defer connection.Close()
	start, err := readLiveClientEvent(rw.Reader)
	if err != nil {
		errors <- err
		return
	}
	var config map[string]json.RawMessage
	if json.Unmarshal(start.Session, &config) != nil || config["model"] != nil || config["audio"] == nil {
		errors <- fmt.Errorf("fork config must inherit model and set WebSocket audio only: %s", start.Session)
		return
	}
	if err := writeServerFrame(rw.Writer, 0x1, []byte(`{"type":"session.started","session":{"id":"fork-session"}}`)); err == nil {
		err = rw.Flush()
	}
	if err != nil {
		errors <- err
		return
	}
	audio, err := readLiveClientEvent(rw.Reader)
	if err != nil || audio.Type != "session.input_audio.append" {
		errors <- fmt.Errorf("fork audio event=%+v err=%v", audio, err)
		return
	}
	closeEvent, err := readLiveClientEvent(rw.Reader)
	if err != nil || closeEvent.Type != "session.close" {
		errors <- fmt.Errorf("fork close event=%+v err=%v", closeEvent, err)
		return
	}
	if err := writeServerFrame(rw.Writer, 0x1, []byte(`{"type":"session.closed"}`)); err == nil {
		err = rw.Flush()
	}
	if err != nil {
		errors <- err
		return
	}
	frame, err := readMaskedClientFrame(rw.Reader)
	if err != nil || frame.opcode != 0x8 {
		errors <- fmt.Errorf("fork websocket close frame=%+v err=%v", frame, err)
	}
}

func hijackLiveTestConnection(writer http.ResponseWriter, request *http.Request) (net.Conn, *bufio.ReadWriter, error) {
	if request.Header.Get("Authorization") != "Bearer TEST_ONLY_LIVE_KEY" {
		return nil, nil, fmt.Errorf("authorization header missing")
	}
	hijacker, ok := writer.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("server cannot hijack")
	}
	connection, rw, err := hijacker.Hijack()
	if err != nil {
		return nil, nil, err
	}
	if _, err = fmt.Fprintf(rw, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", websocketAccept(request.Header.Get("Sec-WebSocket-Key"))); err == nil {
		err = rw.Flush()
	}
	if err != nil {
		connection.Close()
		return nil, nil, err
	}
	return connection, rw, nil
}

func TestOpenAILiveStoredSessionRejectsMissingServerSessionID(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, rw, err := hijackLiveTestConnection(writer, request)
		if err != nil {
			return
		}
		defer connection.Close()
		if _, err := readLiveClientEvent(rw.Reader); err != nil {
			return
		}
		_ = writeServerFrame(rw.Writer, 0x1, []byte(`{"type":"session.started"}`))
		_ = rw.Flush()
	}))
	defer server.Close()
	provider := NewOpenAILive(domain.ProviderConfig{
		ID: "live", Kind: domain.ProviderOpenAILive, Enabled: true,
		BaseURL:  strings.Replace(server.URL, "https://", "wss://", 1),
		Realtime: &domain.RealtimeProviderConfig{Store: true},
	}, nil)
	provider.connector.tlsConfig = &tls.Config{InsecureSkipVerify: true} // Local test certificate only.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := provider.OpenRealtime(ctx, RealtimeOpenRequest{ProviderID: "live", ModelID: "gpt-live-1", Credential: "TEST_ONLY_LIVE_KEY"})
	if err == nil || OutcomeOf(err) != domain.OutcomeInvalidResponse {
		t.Fatalf("stored session without ID accepted: %v", err)
	}
}
