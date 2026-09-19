package providers

// This file implements the bounded RFC6455 client transport shared by realtime
// adapters. Provider protocol semantics live above this socket boundary.

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const realtimeWebSocketMaxFrameBytes = 4 << 20
const websocketGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

type realtimeWebSocket struct {
	connection net.Conn
	reader     *bufio.Reader
	writeMu    sync.Mutex
	readMu     sync.Mutex
	close      sync.Once
}

func dialRealtimeWebSocket(ctx context.Context, connector realtimeConnector, endpoint string, headers http.Header) (*realtimeWebSocket, error) {
	target, err := parseRealtimeEndpoint(endpoint)
	if err != nil {
		return nil, err
	}
	connection, err := connector.DialTLS(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	reader := bufio.NewReader(connection)
	if err := performWebSocketUpgrade(ctx, connection, reader, target, headers); err != nil {
		connection.Close()
		return nil, err
	}
	return &realtimeWebSocket{connection: connection, reader: reader}, nil
}

func performWebSocketUpgrade(ctx context.Context, connection net.Conn, reader *bufio.Reader, target *url.URL, headers http.Header) error {
	keyBytes := make([]byte, 16)
	if _, err := rand.Read(keyBytes); err != nil {
		return fmt.Errorf("realtime websocket key: %w", err)
	}
	key := base64.StdEncoding.EncodeToString(keyBytes)
	requestHeaders := headers.Clone()
	if requestHeaders == nil {
		requestHeaders = make(http.Header)
	}
	requestHeaders.Set("Upgrade", "websocket")
	requestHeaders.Set("Connection", "Upgrade")
	requestHeaders.Set("Sec-WebSocket-Version", "13")
	requestHeaders.Set("Sec-WebSocket-Key", key)

	request := &http.Request{
		Method: http.MethodGet,
		URL:    target,
		Host:   target.Host,
		Header: requestHeaders,
	}
	cleanup := applyConnectionDeadline(ctx, connection)
	defer cleanup()
	if err := request.Write(connection); err != nil {
		return contextAwareNetworkError(ctx, "realtime websocket request", err)
	}
	response, err := http.ReadResponse(reader, request)
	if err != nil {
		return contextAwareNetworkError(ctx, "realtime websocket response", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusSwitchingProtocols {
		return fmt.Errorf("realtime websocket upgrade rejected: HTTP %d", response.StatusCode)
	}
	if !headerHasToken(response.Header, "Upgrade", "websocket") || !headerHasToken(response.Header, "Connection", "upgrade") {
		return fmt.Errorf("realtime websocket upgrade headers are invalid")
	}
	if response.Header.Get("Sec-WebSocket-Accept") != websocketAccept(key) {
		return fmt.Errorf("realtime websocket accept key mismatch")
	}
	return nil
}

func websocketAccept(key string) string {
	sum := sha1.Sum([]byte(key + websocketGUID))
	return base64.StdEncoding.EncodeToString(sum[:])
}

func headerHasToken(header http.Header, name, token string) bool {
	for _, value := range header.Values(name) {
		for _, part := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(part), token) {
				return true
			}
		}
	}
	return false
}

func (socket *realtimeWebSocket) WriteText(ctx context.Context, text string) error {
	if socket == nil || socket.connection == nil {
		return fmt.Errorf("realtime websocket is closed")
	}
	if !utf8.ValidString(text) {
		return fmt.Errorf("realtime websocket text is not valid UTF-8")
	}
	return socket.writeFrame(ctx, 0x1, []byte(text))
}

func (socket *realtimeWebSocket) ReadText(ctx context.Context) (string, error) {
	if socket == nil || socket.connection == nil {
		return "", fmt.Errorf("realtime websocket is closed")
	}
	socket.readMu.Lock()
	defer socket.readMu.Unlock()
	for {
		frame, err := socket.readFrame(ctx)
		if err != nil {
			return "", err
		}
		switch frame.opcode {
		case 0x1:
			if !utf8.Valid(frame.payload) {
				return "", fmt.Errorf("realtime websocket received invalid UTF-8")
			}
			return string(frame.payload), nil
		case 0x8:
			return "", io.EOF
		case 0x9:
			if err := socket.writeFrame(ctx, 0xA, frame.payload); err != nil {
				return "", err
			}
		case 0xA:
			continue
		default:
			return "", fmt.Errorf("realtime websocket received unsupported opcode 0x%x", frame.opcode)
		}
	}
}

func (socket *realtimeWebSocket) Close() error {
	if socket == nil {
		return nil
	}
	var closeErr error
	socket.close.Do(func() {
		if socket.connection == nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		_ = socket.writeFrame(ctx, 0x8, nil)
		cancel()
		closeErr = socket.connection.Close()
	})
	return closeErr
}

type realtimeWebSocketFrame struct {
	opcode  byte
	payload []byte
}

func (socket *realtimeWebSocket) readFrame(ctx context.Context) (realtimeWebSocketFrame, error) {
	cleanup := applyConnectionDeadline(ctx, socket.connection)
	defer cleanup()
	first, err := socket.reader.ReadByte()
	if err != nil {
		return realtimeWebSocketFrame{}, contextAwareNetworkError(ctx, "realtime websocket frame", err)
	}
	second, err := socket.reader.ReadByte()
	if err != nil {
		return realtimeWebSocketFrame{}, contextAwareNetworkError(ctx, "realtime websocket frame", err)
	}
	if first&0x70 != 0 || first&0x80 == 0 {
		return realtimeWebSocketFrame{}, fmt.Errorf("realtime websocket fragmentation/extensions are unsupported")
	}
	if second&0x80 != 0 {
		return realtimeWebSocketFrame{}, fmt.Errorf("realtime websocket server frame must not be masked")
	}
	opcode := first & 0x0F
	length, err := readWebSocketLength(socket.reader, second&0x7F)
	if err != nil {
		return realtimeWebSocketFrame{}, err
	}
	if length > realtimeWebSocketMaxFrameBytes {
		return realtimeWebSocketFrame{}, fmt.Errorf("realtime websocket frame exceeds %d bytes", realtimeWebSocketMaxFrameBytes)
	}
	if opcode >= 0x8 && length > 125 {
		return realtimeWebSocketFrame{}, fmt.Errorf("realtime websocket control frame is too large")
	}
	payload := make([]byte, int(length))
	if _, err := io.ReadFull(socket.reader, payload); err != nil {
		return realtimeWebSocketFrame{}, contextAwareNetworkError(ctx, "realtime websocket payload", err)
	}
	return realtimeWebSocketFrame{opcode: opcode, payload: payload}, nil
}

func readWebSocketLength(reader io.Reader, marker byte) (uint64, error) {
	switch marker {
	case 126:
		var raw [2]byte
		if _, err := io.ReadFull(reader, raw[:]); err != nil {
			return 0, err
		}
		return uint64(binary.BigEndian.Uint16(raw[:])), nil
	case 127:
		var raw [8]byte
		if _, err := io.ReadFull(reader, raw[:]); err != nil {
			return 0, err
		}
		value := binary.BigEndian.Uint64(raw[:])
		if value>>63 != 0 {
			return 0, fmt.Errorf("realtime websocket frame length is invalid")
		}
		return value, nil
	default:
		return uint64(marker), nil
	}
}

func (socket *realtimeWebSocket) writeFrame(ctx context.Context, opcode byte, payload []byte) error {
	if len(payload) > realtimeWebSocketMaxFrameBytes {
		return fmt.Errorf("realtime websocket frame exceeds %d bytes", realtimeWebSocketMaxFrameBytes)
	}
	if opcode >= 0x8 && len(payload) > 125 {
		return fmt.Errorf("realtime websocket control frame is too large")
	}
	mask := [4]byte{}
	if _, err := rand.Read(mask[:]); err != nil {
		return fmt.Errorf("realtime websocket mask: %w", err)
	}
	frame := buildMaskedWebSocketFrame(opcode, payload, mask)

	socket.writeMu.Lock()
	defer socket.writeMu.Unlock()
	cleanup := applyConnectionDeadline(ctx, socket.connection)
	defer cleanup()
	if err := writeAll(socket.connection, frame); err != nil {
		return contextAwareNetworkError(ctx, "realtime websocket write", err)
	}
	return nil
}

func buildMaskedWebSocketFrame(opcode byte, payload []byte, mask [4]byte) []byte {
	headerLength := 2 + 4
	switch {
	case len(payload) >= 65536:
		headerLength += 8
	case len(payload) >= 126:
		headerLength += 2
	}
	frame := make([]byte, headerLength+len(payload))
	frame[0] = 0x80 | opcode
	offset := 2
	switch {
	case len(payload) >= 65536:
		frame[1] = 0x80 | 127
		binary.BigEndian.PutUint64(frame[2:10], uint64(len(payload)))
		offset = 10
	case len(payload) >= 126:
		frame[1] = 0x80 | 126
		binary.BigEndian.PutUint16(frame[2:4], uint16(len(payload)))
		offset = 4
	default:
		frame[1] = 0x80 | byte(len(payload))
	}
	copy(frame[offset:offset+4], mask[:])
	offset += 4
	for i, value := range payload {
		frame[offset+i] = value ^ mask[i%4]
	}
	return frame
}

func writeAll(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		written, err := writer.Write(data)
		if err != nil {
			return err
		}
		if written <= 0 {
			return io.ErrUnexpectedEOF
		}
		data = data[written:]
	}
	return nil
}

func applyConnectionDeadline(ctx context.Context, connection net.Conn) func() {
	if deadline, ok := ctx.Deadline(); ok {
		_ = connection.SetDeadline(deadline)
	}
	stop := context.AfterFunc(ctx, func() { _ = connection.SetDeadline(time.Now()) })
	return func() {
		stop()
		_ = connection.SetDeadline(time.Time{})
	}
}

func contextAwareNetworkError(ctx context.Context, operation string, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if deadline, ok := ctx.Deadline(); ok && !time.Now().Before(deadline) {
		return context.DeadlineExceeded
	}
	return fmt.Errorf("%s: %w", operation, err)
}
