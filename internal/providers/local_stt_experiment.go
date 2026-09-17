package providers

// This file is an experiment boundary for a future native local STT helper.
// It is deliberately not registered as a Provider: no whisper.cpp binary or
// model weights ship with this implementation yet.

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	"aioverlay/internal/childprocess"
	"aioverlay/internal/domain"
)

const localSTTMaxPayloadBytes = 12 << 20
const localSTTMaxResponseBytes = 2 << 20

var errLocalSTTHelperClosed = errors.New("local STT helper closed")

type localSTTWireRequest struct {
	Type       string `json:"type"`
	ID         string `json:"id"`
	SampleRate int    `json:"sample_rate,omitempty"`
	Channels   int    `json:"channels,omitempty"`
	Samples    int    `json:"samples,omitempty"`
}

type localSTTWireResponse struct {
	Type    string `json:"type"`
	ID      string `json:"id"`
	Text    string `json:"text,omitempty"`
	Message string `json:"message,omitempty"`
}

// LocalSTTExperiment proves the intended IPC/lifetime contract only. The helper
// is long-lived, receives raw PCM16 in memory and can receive cancellation by ID.
type LocalSTTExperiment struct {
	process *childprocess.Process
	stdin   io.WriteCloser

	writeGate chan struct{}
	sequence  atomic.Uint64

	mu      sync.Mutex
	pending map[string]chan localSTTWireResponse
	closed  bool
	close   sync.Once
}

func StartLocalSTTExperiment(command *exec.Cmd) (*LocalSTTExperiment, error) {
	if command == nil {
		return nil, fmt.Errorf("local STT helper command is required")
	}
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("local STT stdin: %w", err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("local STT stdout: %w", err)
	}
	process, err := childprocess.Start(command)
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("start local STT helper: %w", err)
	}

	runtime := &LocalSTTExperiment{
		process: process, stdin: stdin,
		writeGate: make(chan struct{}, 1),
		pending:   map[string]chan localSTTWireResponse{},
	}
	go runtime.readResponses(stdout)
	return runtime, nil
}

func (runtime *LocalSTTExperiment) Alive() bool {
	return runtime != nil && runtime.process != nil && runtime.process.Alive()
}

func (runtime *LocalSTTExperiment) Transcribe(ctx context.Context, clip domain.AudioClip) (string, error) {
	if err := validateLocalSTTClip(clip); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	id := fmt.Sprintf("stt-%d", runtime.sequence.Add(1))
	result := make(chan localSTTWireResponse, 1)
	if err := runtime.register(id, result); err != nil {
		return "", err
	}
	defer runtime.unregister(id)

	request := localSTTWireRequest{
		Type: "transcribe", ID: id,
		SampleRate: clip.SampleRate, Channels: clip.Channels, Samples: len(clip.PCM16),
	}
	if err := runtime.writeTranscription(ctx, request, clip.PCM16); err != nil {
		return "", err
	}

	select {
	case response := <-result:
		if response.Type == "error" {
			return "", fmt.Errorf("local STT helper: %s", response.Message)
		}
		if response.Type != "result" {
			return "", fmt.Errorf("local STT helper returned %q", response.Type)
		}
		return response.Text, nil
	case <-ctx.Done():
		runtime.sendCancel(id)
		return "", ctx.Err()
	}
}

func validateLocalSTTClip(clip domain.AudioClip) error {
	if clip.SampleRate < 8000 || clip.SampleRate > 192000 || clip.Channels != 1 || len(clip.PCM16) == 0 {
		return fmt.Errorf("invalid local STT PCM16 input")
	}
	if len(clip.PCM16) > localSTTMaxPayloadBytes/2 {
		return fmt.Errorf("local STT PCM payload exceeds %d bytes", localSTTMaxPayloadBytes)
	}
	return nil
}

func (runtime *LocalSTTExperiment) register(id string, result chan localSTTWireResponse) error {
	if runtime == nil {
		return errLocalSTTHelperClosed
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.closed || !runtime.Alive() {
		return errLocalSTTHelperClosed
	}
	runtime.pending[id] = result
	return nil
}

func (runtime *LocalSTTExperiment) unregister(id string) {
	runtime.mu.Lock()
	delete(runtime.pending, id)
	runtime.mu.Unlock()
}

func (runtime *LocalSTTExperiment) writeTranscription(ctx context.Context, request localSTTWireRequest, samples []int16) error {
	if err := runtime.acquireWriter(ctx); err != nil {
		return err
	}
	defer runtime.releaseWriter()
	if err := runtime.writeJSON(request); err != nil {
		runtime.Close()
		return err
	}
	if err := writePCM16(ctx, runtime.stdin, samples); err != nil {
		// A partial binary frame cannot be recovered safely. Closing the helper is
		// preferable to interpreting following control bytes as audio.
		runtime.Close()
		return err
	}
	return nil
}

func (runtime *LocalSTTExperiment) sendCancel(id string) {
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	if err := runtime.acquireWriter(ctx); err != nil {
		return
	}
	defer runtime.releaseWriter()
	_ = runtime.writeJSON(localSTTWireRequest{Type: "cancel", ID: id})
}

func (runtime *LocalSTTExperiment) acquireWriter(ctx context.Context) error {
	select {
	case runtime.writeGate <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (runtime *LocalSTTExperiment) releaseWriter() {
	<-runtime.writeGate
}

func (runtime *LocalSTTExperiment) writeJSON(value localSTTWireRequest) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if _, err := runtime.stdin.Write(raw); err != nil {
		return fmt.Errorf("local STT IPC write: %w", err)
	}
	return nil
}

func writePCM16(ctx context.Context, writer io.Writer, samples []int16) error {
	const samplesPerChunk = 4096
	buffer := make([]byte, samplesPerChunk*2)
	for offset := 0; offset < len(samples); offset += samplesPerChunk {
		if err := ctx.Err(); err != nil {
			return err
		}
		end := min(offset+samplesPerChunk, len(samples))
		chunk := samples[offset:end]
		for i, sample := range chunk {
			binary.LittleEndian.PutUint16(buffer[i*2:], uint16(sample))
		}
		if _, err := writer.Write(buffer[:len(chunk)*2]); err != nil {
			return fmt.Errorf("local STT PCM write: %w", err)
		}
	}
	return nil
}

func (runtime *LocalSTTExperiment) readResponses(reader io.Reader) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), localSTTMaxResponseBytes)
	for scanner.Scan() {
		var response localSTTWireResponse
		if json.Unmarshal(scanner.Bytes(), &response) != nil || response.ID == "" {
			continue
		}
		runtime.mu.Lock()
		result := runtime.pending[response.ID]
		runtime.mu.Unlock()
		if result == nil {
			continue
		}
		select {
		case result <- response:
		default:
		}
	}
	runtime.failPending(errLocalSTTHelperClosed)
}

func (runtime *LocalSTTExperiment) failPending(err error) {
	runtime.mu.Lock()
	pending := runtime.pending
	runtime.pending = map[string]chan localSTTWireResponse{}
	runtime.closed = true
	runtime.mu.Unlock()
	for id, result := range pending {
		select {
		case result <- localSTTWireResponse{Type: "error", ID: id, Message: err.Error()}:
		default:
		}
	}
}

func (runtime *LocalSTTExperiment) Close() {
	if runtime == nil {
		return
	}
	runtime.close.Do(func() {
		runtime.mu.Lock()
		runtime.closed = true
		runtime.mu.Unlock()
		_ = runtime.stdin.Close()
		if runtime.process != nil {
			runtime.process.Stop()
		}
		runtime.failPending(errLocalSTTHelperClosed)
	})
}
