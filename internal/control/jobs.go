package control

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"aioverlay/internal/benchmark"
	"aioverlay/internal/domain"
	"aioverlay/internal/engine"
)

type liveBackend interface {
	Run(context.Context, string, engine.Input, engine.RunOptions) (engine.Result, error)
}

type Job struct {
	ID         string                `json:"id"`
	Running    bool                  `json:"running"`
	Text       string                `json:"text"`
	Error      string                `json:"error,omitempty"`
	Trace      domain.ExecutionTrace `json:"trace"`
	Started    time.Time             `json:"started"`
	DurationMS int64                 `json:"duration_ms"`
}

type JobManager struct {
	mu     sync.Mutex
	job    Job
	cancel context.CancelFunc
}

func (j *JobManager) Snapshot() Job {
	j.mu.Lock()
	defer j.mu.Unlock()
	copy := j.job
	copy.Trace.Events = append([]domain.TraceEvent(nil), j.job.Trace.Events...)
	if copy.Running {
		copy.DurationMS = time.Since(copy.Started).Milliseconds()
	}
	return copy
}
func (j *JobManager) Cancel() {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.cancel != nil {
		j.cancel()
	}
}
func (j *JobManager) Start(parent context.Context, b liveBackend, pipeline string, input engine.Input) (string, error) {
	j.mu.Lock()
	if j.job.Running {
		j.mu.Unlock()
		return "", fmt.Errorf("a run is already active; stop it first")
	}
	ctx, cancel := context.WithCancel(parent)
	id := fmt.Sprintf("run-%d", time.Now().UnixNano())
	j.job = Job{ID: id, Running: true, Started: time.Now(), Trace: domain.ExecutionTrace{PipelineID: pipeline, Events: []domain.TraceEvent{}}}
	j.cancel = cancel
	j.mu.Unlock()
	go func() {
		defer cancel()
		result, err := b.Run(ctx, pipeline, input, engine.RunOptions{
			OnDelta: func(delta string) { j.mu.Lock(); j.job.Text += delta; j.mu.Unlock() },
			OnReset: func() { j.mu.Lock(); j.job.Text = ""; j.mu.Unlock() },
			OnTrace: func(ev domain.TraceEvent) {
				j.mu.Lock()
				j.job.Trace.Events = append(j.job.Trace.Events, ev)
				j.mu.Unlock()
			},
		})
		j.mu.Lock()
		defer j.mu.Unlock()
		j.job.Running = false
		j.job.Trace = result.Trace
		j.job.DurationMS = time.Since(j.job.Started).Milliseconds()
		if err != nil {
			j.job.Error = err.Error()
		} else {
			j.job.Text = result.Text
		}
	}()
	return id, nil
}

func (s *Server) handleJob(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, s.jobs.Snapshot())
	case http.MethodDelete:
		s.jobs.Cancel()
		writeJSON(w, map[string]bool{"ok": true})
	case http.MethodPost:
		var req struct {
			PipelineID string `json:"pipeline_id"`
			Image      string `json:"image_base64"`
			Text       string `json:"text"`
			Source     string `json:"source"`
		}
		if err := decodeJSON(r, &req, 20<<20); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		b, ok := s.backend.(liveBackend)
		if !ok {
			http.Error(w, "live execution unsupported", 501)
			return
		}
		input := engine.Input{Text: req.Text}
		var err error
		switch req.Source {
		case "synthetic":
			input.ImageJPEG, err = benchmark.ImageJPEG()
		case "screen":
			if s.Capture == nil {
				err = fmt.Errorf("screen capture is available only on Windows")
			} else {
				input.ImageJPEG, err = s.Capture()
			}
		default:
			if req.Image != "" {
				input.ImageJPEG, err = base64.StdEncoding.DecodeString(req.Image)
			}
		}
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		// parent is the application lifetime, not an HTTP request that ends at 202.
		id, err := s.jobs.Start(s.ctx, b, req.PipelineID, input)
		if err != nil {
			http.Error(w, err.Error(), 409)
			return
		}
		writeJSON(w, map[string]string{"id": id})
	default:
		w.WriteHeader(405)
	}
}

func (s *Server) handleCapture(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.WriteHeader(405)
		return
	}
	if s.Capture == nil {
		http.Error(w, "Screen capture requires Windows 10/11.", 501)
		return
	}
	start := time.Now()
	image, err := s.Capture()
	if err != nil {
		http.Error(w, err.Error(), 502)
		return
	}
	writeJSON(w, map[string]any{"image_base64": base64.StdEncoding.EncodeToString(image), "duration_ms": time.Since(start).Milliseconds(), "bytes": len(image), "method": "GDI / CreateDIBSection · 32-bit top-down BGRA"})
}

// Keep imported JSON data as a value, not executable JavaScript.
var _ = json.Valid

func (s *Server) StartImage(image []byte) (string, error) {
	b, ok := s.backend.(liveBackend)
	if !ok {
		return "", fmt.Errorf("live execution unavailable")
	}
	return s.jobs.Start(s.ctx, b, "", engine.Input{ImageJPEG: image})
}
func (s *Server) CurrentJob() Job { return s.jobs.Snapshot() }
