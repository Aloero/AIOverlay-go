package providers

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRealtimeWebSocketExperimentHandshakeFramesPingAndClose(t *testing.T) {
	serverDone := make(chan error, 1)
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/realtime" || request.URL.Query().Get("model") != "fixture" {
			serverDone <- fmt.Errorf("unexpected websocket target %s", request.URL.String())
			return
		}
		if request.Header.Get("Authorization") != "Bearer synthetic" || !headerHasToken(request.Header, "Upgrade", "websocket") {
			serverDone <- fmt.Errorf("websocket headers missing")
			return
		}
		hijacker, ok := writer.(http.Hijacker)
		if !ok {
			serverDone <- fmt.Errorf("test server cannot hijack connection")
			return
		}
		connection, readWriter, err := hijacker.Hijack()
		if err != nil {
			serverDone <- err
			return
		}
		defer connection.Close()

		key := request.Header.Get("Sec-WebSocket-Key")
		_, err = fmt.Fprintf(readWriter, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", websocketAccept(key))
		if err == nil {
			err = readWriter.Flush()
		}
		if err != nil {
			serverDone <- err
			return
		}

		frame, err := readMaskedClientFrame(readWriter.Reader)
		if err != nil || frame.opcode != 0x1 || string(frame.payload) != "hello" {
			serverDone <- fmt.Errorf("client text frame=%+v err=%v", frame, err)
			return
		}
		if err := writeServerFrame(readWriter.Writer, 0x9, []byte("probe")); err != nil {
			serverDone <- err
			return
		}
		if err := readWriter.Flush(); err != nil {
			serverDone <- err
			return
		}
		pong, err := readMaskedClientFrame(readWriter.Reader)
		if err != nil || pong.opcode != 0xA || string(pong.payload) != "probe" {
			serverDone <- fmt.Errorf("client pong=%+v err=%v", pong, err)
			return
		}
		if err := writeServerFrame(readWriter.Writer, 0x1, []byte("world")); err != nil {
			serverDone <- err
			return
		}
		if err := readWriter.Flush(); err != nil {
			serverDone <- err
			return
		}
		closeFrame, err := readMaskedClientFrame(readWriter.Reader)
		if err != nil || closeFrame.opcode != 0x8 {
			serverDone <- fmt.Errorf("client close frame=%+v err=%v", closeFrame, err)
			return
		}
		serverDone <- nil
	}))
	defer server.Close()

	endpoint := strings.Replace(server.URL, "https://", "wss://", 1) + "/realtime?model=fixture"
	connector := newRealtimeConnector(nil, time.Second)
	connector.tlsConfig = &tls.Config{InsecureSkipVerify: true} // Test-only local certificate.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	socket, err := dialRealtimeWebSocketExperiment(ctx, connector, endpoint, http.Header{"Authorization": {"Bearer synthetic"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := socket.WriteText(ctx, "hello"); err != nil {
		socket.Close()
		t.Fatal(err)
	}
	text, err := socket.ReadText(ctx)
	if err != nil || text != "world" {
		socket.Close()
		t.Fatalf("server text=%q err=%v", text, err)
	}
	if err := socket.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("websocket server did not observe close")
	}
}

func TestRealtimeWebSocketExperimentFailsClosedBeforeHandshake(t *testing.T) {
	var dials atomic.Int32
	connector := newRealtimeConnector(testProxyResolver{err: errors.New("tunnel unavailable")}, time.Second)
	connector.dialContext = func(context.Context, string, string) (net.Conn, error) {
		dials.Add(1)
		return nil, fmt.Errorf("unexpected direct dial")
	}
	_, err := dialRealtimeWebSocketExperiment(context.Background(), connector, "wss://provider.example/realtime", nil)
	if err == nil || !strings.Contains(err.Error(), "AI VPN unavailable") {
		t.Fatalf("fail-closed websocket error=%v", err)
	}
	if dials.Load() != 0 {
		t.Fatalf("websocket bypassed VPN resolver with %d direct dials", dials.Load())
	}
}

func TestRealtimeWebSocketExperimentBoundsFramesAndReadCancellation(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	socket := &realtimeWebSocketExperiment{connection: client, reader: bufio.NewReader(client)}
	defer socket.Close()

	tooLarge := strings.Repeat("x", realtimeWebSocketMaxFrameBytes+1)
	if err := socket.WriteText(context.Background(), tooLarge); err == nil {
		t.Fatal("oversized websocket frame accepted")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := socket.ReadText(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("blocked websocket read ignored context: %v", err)
	}
}

func readMaskedClientFrame(reader *bufio.Reader) (realtimeWebSocketFrame, error) {
	first, err := reader.ReadByte()
	if err != nil {
		return realtimeWebSocketFrame{}, err
	}
	second, err := reader.ReadByte()
	if err != nil {
		return realtimeWebSocketFrame{}, err
	}
	if first&0x80 == 0 || second&0x80 == 0 {
		return realtimeWebSocketFrame{}, fmt.Errorf("client frame is not final and masked")
	}
	length, err := readWebSocketLength(reader, second&0x7F)
	if err != nil {
		return realtimeWebSocketFrame{}, err
	}
	if length > realtimeWebSocketMaxFrameBytes {
		return realtimeWebSocketFrame{}, fmt.Errorf("client frame is too large")
	}
	var mask [4]byte
	if _, err := io.ReadFull(reader, mask[:]); err != nil {
		return realtimeWebSocketFrame{}, err
	}
	payload := make([]byte, int(length))
	if _, err := io.ReadFull(reader, payload); err != nil {
		return realtimeWebSocketFrame{}, err
	}
	for index := range payload {
		payload[index] ^= mask[index%4]
	}
	return realtimeWebSocketFrame{opcode: first & 0x0F, payload: payload}, nil
}

func writeServerFrame(writer io.Writer, opcode byte, payload []byte) error {
	header := []byte{0x80 | opcode, 0}
	switch {
	case len(payload) >= 65536:
		header[1] = 127
		extra := make([]byte, 8)
		binary.BigEndian.PutUint64(extra, uint64(len(payload)))
		header = append(header, extra...)
	case len(payload) >= 126:
		header[1] = 126
		extra := make([]byte, 2)
		binary.BigEndian.PutUint16(extra, uint16(len(payload)))
		header = append(header, extra...)
	default:
		header[1] = byte(len(payload))
	}
	return writeAll(writer, append(header, payload...))
}
