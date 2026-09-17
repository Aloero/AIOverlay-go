package providers

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"

	"aioverlay/internal/domain"
)

func TestLocalSTTExperimentHelperProcess(t *testing.T) {
	if os.Getenv("AIO_LOCAL_STT_HELPER") != "1" {
		return
	}
	if err := runSyntheticLocalSTTHelper(os.Stdin, os.Stdout); err != nil {
		t.Fatal(err)
	}
}

func TestLocalSTTExperimentReusesHelperAndCancelsByRequestID(t *testing.T) {
	runtime := startSyntheticLocalSTT(t)
	defer runtime.Close()

	first := domain.AudioClip{PCM16: make([]int16, 160), SampleRate: 16000, Channels: 1}
	text, err := runtime.Transcribe(context.Background(), first)
	if err != nil || text != "samples=160" {
		t.Fatalf("first transcription text=%q err=%v", text, err)
	}
	if !runtime.Alive() {
		t.Fatal("helper exited after first request")
	}

	slow := domain.AudioClip{PCM16: make([]int16, 160), SampleRate: 16000, Channels: 1}
	slow.PCM16[0] = 9999
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err = runtime.Transcribe(ctx, slow)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancelled transcription error=%v", err)
	}
	if time.Since(started) > 500*time.Millisecond {
		t.Fatal("local STT cancellation was not bounded")
	}
	if !runtime.Alive() {
		t.Fatal("request cancellation killed the shared helper")
	}

	text, err = runtime.Transcribe(context.Background(), first)
	if err != nil || text != "samples=160" {
		t.Fatalf("helper was not reusable after cancellation: text=%q err=%v", text, err)
	}
}

func TestLocalSTTExperimentRejectsInvalidAndOversizedPCM(t *testing.T) {
	if err := validateLocalSTTClip(domain.AudioClip{PCM16: []int16{1}, SampleRate: 16000, Channels: 2}); err == nil {
		t.Fatal("multichannel local STT input accepted")
	}
	oversized := domain.AudioClip{PCM16: make([]int16, localSTTMaxPayloadBytes/2+1), SampleRate: 16000, Channels: 1}
	if err := validateLocalSTTClip(oversized); err == nil {
		t.Fatal("oversized local STT payload accepted")
	}
}

func startSyntheticLocalSTT(t *testing.T) *LocalSTTExperiment {
	t.Helper()
	command := exec.Command(os.Args[0], "-test.run=^TestLocalSTTExperimentHelperProcess$")
	command.Env = append(os.Environ(), "AIO_LOCAL_STT_HELPER=1")
	runtime, err := StartLocalSTTExperiment(command)
	if err != nil {
		t.Fatal(err)
	}
	return runtime
}

type syntheticHelper struct {
	writer io.Writer

	writeMu sync.Mutex
	mu      sync.Mutex
	cancel  map[string]chan struct{}
}

func runSyntheticLocalSTTHelper(input io.Reader, output io.Writer) error {
	helper := &syntheticHelper{writer: output, cancel: map[string]chan struct{}{}}
	reader := bufio.NewReader(input)
	for {
		line, err := reader.ReadBytes('\n')
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		var request localSTTWireRequest
		if err := json.Unmarshal(line, &request); err != nil {
			return fmt.Errorf("decode request: %w", err)
		}
		switch request.Type {
		case "transcribe":
			payload := make([]byte, request.Samples*2)
			if _, err := io.ReadFull(reader, payload); err != nil {
				return err
			}
			helper.startTranscription(request, payload)
		case "cancel":
			helper.cancelRequest(request.ID)
		default:
			return fmt.Errorf("unknown request %q", request.Type)
		}
	}
}

func (helper *syntheticHelper) startTranscription(request localSTTWireRequest, payload []byte) {
	cancelled := make(chan struct{})
	helper.mu.Lock()
	helper.cancel[request.ID] = cancelled
	helper.mu.Unlock()

	go func() {
		defer helper.removeRequest(request.ID)
		slow := len(payload) >= 2 && int16(binary.LittleEndian.Uint16(payload[:2])) == 9999
		if slow {
			select {
			case <-cancelled:
				return
			case <-time.After(5 * time.Second):
			}
		}
		helper.writeResponse(localSTTWireResponse{Type: "result", ID: request.ID, Text: fmt.Sprintf("samples=%d", request.Samples)})
	}()
}

func (helper *syntheticHelper) cancelRequest(id string) {
	helper.mu.Lock()
	cancelled := helper.cancel[id]
	if cancelled != nil {
		delete(helper.cancel, id)
	}
	helper.mu.Unlock()
	if cancelled != nil {
		close(cancelled)
	}
}

func (helper *syntheticHelper) removeRequest(id string) {
	helper.mu.Lock()
	delete(helper.cancel, id)
	helper.mu.Unlock()
}

func (helper *syntheticHelper) writeResponse(response localSTTWireResponse) {
	raw, _ := json.Marshal(response)
	raw = append(raw, '\n')
	helper.writeMu.Lock()
	_, _ = helper.writer.Write(raw)
	helper.writeMu.Unlock()
}

func TestLocalSTTExperimentConcurrentSoakLeavesNoPendingRequests(t *testing.T) {
	runtime := startSyntheticLocalSTT(t)

	const workers = 8
	const requestsPerWorker = 25
	var group sync.WaitGroup
	errCh := make(chan error, workers)

	for worker := 0; worker < workers; worker++ {
		group.Add(1)
		go func(worker int) {
			defer group.Done()
			for request := 0; request < requestsPerWorker; request++ {
				clip := domain.AudioClip{PCM16: make([]int16, 160), SampleRate: 16000, Channels: 1}
				if (worker+request)%7 == 0 {
					clip.PCM16[0] = 9999
					ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
					_, err := runtime.Transcribe(ctx, clip)
					cancel()
					if err != nil && !errors.Is(err, context.DeadlineExceeded) {
						errCh <- fmt.Errorf("worker %d request %d cancel: %w", worker, request, err)
						return
					}
					continue
				}

				text, err := runtime.Transcribe(context.Background(), clip)
				if err != nil || text != "samples=160" {
					errCh <- fmt.Errorf("worker %d request %d: text=%q err=%v", worker, request, text, err)
					return
				}
			}
		}(worker)
	}

	group.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			runtime.Close()
			t.Fatal(err)
		}
	}

	runtime.mu.Lock()
	pending := len(runtime.pending)
	runtime.mu.Unlock()
	if pending != 0 {
		runtime.Close()
		t.Fatalf("pending request leak after soak: %d", pending)
	}
	if !runtime.Alive() {
		runtime.Close()
		t.Fatal("shared local STT helper died during soak")
	}

	clip := domain.AudioClip{PCM16: make([]int16, 160), SampleRate: 16000, Channels: 1}
	text, err := runtime.Transcribe(context.Background(), clip)
	if err != nil || text != "samples=160" {
		runtime.Close()
		t.Fatalf("helper was not reusable after soak: text=%q err=%v", text, err)
	}

	runtime.Close()
	if runtime.Alive() {
		t.Fatal("local STT helper remained alive after Close")
	}
}
