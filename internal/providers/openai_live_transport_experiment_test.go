package providers

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestOpenAILiveTransportExperimentUsesExplicitEndpointAuthAndSessionStart(t *testing.T) {
	serverDone := make(chan error, 1)
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/explicit-live-primary" || request.URL.Query().Get("fixture") != "1" {
			serverDone <- fmt.Errorf("unexpected Live path %s", request.URL.String())
			return
		}
		if request.Header.Get("Authorization") != "Bearer TEST_ONLY_LIVE_KEY" {
			serverDone <- fmt.Errorf("Live authorization header missing")
			return
		}
		hijacker, ok := writer.(http.Hijacker)
		if !ok {
			serverDone <- fmt.Errorf("test server cannot hijack")
			return
		}
		connection, readWriter, err := hijacker.Hijack()
		if err != nil {
			serverDone <- err
			return
		}
		defer connection.Close()

		_, err = fmt.Fprintf(readWriter, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", websocketAccept(request.Header.Get("Sec-WebSocket-Key")))
		if err == nil {
			err = readWriter.Flush()
		}
		if err != nil {
			serverDone <- err
			return
		}

		frame, err := readMaskedClientFrame(readWriter.Reader)
		if err != nil || frame.opcode != 0x1 {
			serverDone <- fmt.Errorf("Live session.start frame=%+v err=%v", frame, err)
			return
		}
		var event openAILiveEnvelope
		if err := json.Unmarshal(frame.payload, &event); err != nil {
			serverDone <- err
			return
		}
		if event.Type != "session.start" || string(event.Session) != `{"model":"gpt-live-1"}` {
			serverDone <- fmt.Errorf("unexpected Live startup event %+v", event)
			return
		}
		ack := []byte(fmt.Sprintf(`{"type":"session.started","client_event_id":%q,"session":{"id":"live_fixture"}}`, event.EventID))
		if err := writeServerFrame(readWriter.Writer, 0x1, ack); err != nil {
			serverDone <- err
			return
		}
		if err := readWriter.Flush(); err != nil {
			serverDone <- err
			return
		}
		closeFrame, err := readMaskedClientFrame(readWriter.Reader)
		if err != nil || closeFrame.opcode != 0x8 {
			serverDone <- fmt.Errorf("Live close frame=%+v err=%v", closeFrame, err)
			return
		}
		serverDone <- nil
	}))
	defer server.Close()

	endpoint := strings.Replace(server.URL, "https://", "wss://", 1) + "/explicit-live-primary?fixture=1"
	connector := newRealtimeConnector(nil, time.Second)
	connector.tlsConfig = &tls.Config{InsecureSkipVerify: true} // Test-only local certificate.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	protocol, err := dialOpenAILiveTransportExperiment(ctx, connector, openAILiveTransportExperimentConfig{
		Endpoint: endpoint, APIKey: "TEST_ONLY_LIVE_KEY",
		Session: json.RawMessage(`{"model":"gpt-live-1"}`), ResponsesDelegated: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	observation, err := protocol.NextServerEvent(ctx)
	if err != nil || observation.Type != "session.started" || !protocol.Started() {
		_ = protocol.Close()
		t.Fatalf("Live transport acknowledgement=%+v err=%v", observation, err)
	}
	if err := protocol.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Live transport server did not finish")
	}
}

func TestOpenAILiveTransportExperimentFailsClosedBeforeWebSocketDial(t *testing.T) {
	var dials atomic.Int32
	connector := newRealtimeConnector(testProxyResolver{err: errors.New("tunnel unavailable")}, time.Second)
	connector.dialContext = func(context.Context, string, string) (net.Conn, error) {
		dials.Add(1)
		return nil, fmt.Errorf("unexpected direct dial")
	}
	_, err := dialOpenAILiveTransportExperiment(context.Background(), connector, openAILiveTransportExperimentConfig{
		Endpoint: "wss://provider.example/live", APIKey: "TEST_ONLY_LIVE_KEY",
		Session: json.RawMessage(`{"model":"gpt-live-1"}`),
	})
	if err == nil || !strings.Contains(err.Error(), "AI VPN unavailable") {
		t.Fatalf("Live fail-closed error=%v", err)
	}
	if dials.Load() != 0 {
		t.Fatalf("Live experiment bypassed VPN resolver with %d direct dials", dials.Load())
	}
}

func TestOpenAILiveTransportExperimentRejectsCredentialInURLAndMissingKeyBeforeDial(t *testing.T) {
	var dials atomic.Int32
	connector := newRealtimeConnector(nil, time.Second)
	connector.dialContext = func(context.Context, string, string) (net.Conn, error) {
		dials.Add(1)
		return nil, fmt.Errorf("should not dial")
	}

	_, err := dialOpenAILiveTransportExperiment(context.Background(), connector, openAILiveTransportExperimentConfig{
		Endpoint: "wss://secret@provider.example/live", APIKey: "TEST_ONLY_LIVE_KEY",
		Session: json.RawMessage(`{"model":"gpt-live-1"}`),
	})
	if err == nil || !strings.Contains(err.Error(), "credential-free wss URL") {
		t.Fatalf("credentialed Live endpoint error=%v", err)
	}
	_, err = dialOpenAILiveTransportExperiment(context.Background(), connector, openAILiveTransportExperimentConfig{
		Endpoint: "wss://provider.example/live", Session: json.RawMessage(`{"model":"gpt-live-1"}`),
	})
	if err == nil || !strings.Contains(err.Error(), "requires an API key") {
		t.Fatalf("missing Live key error=%v", err)
	}
	if dials.Load() != 0 {
		t.Fatalf("invalid Live config reached network: dials=%d", dials.Load())
	}
}
