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
	CancelRequested bool                  `json:"cancel_requested"`
	Purpose         string                `json:"purpose"`
	Cleared         bool                  `json:"cleared"`
	Phase           string                `json:"phase"`
	Benchmark       *BenchmarkReport      `json:"benchmark,omitempty"`
	ID              string                `json:"id"`
	Running         bool                  `json:"running"`
	Text            string                `json:"text"`
	Error           string                `json:"error,omitempty"`
	Trace           domain.ExecutionTrace `json:"trace"`
	Started         time.Time             `json:"started"`
	DurationMS      int64                 `json:"duration_ms"`
}

type JobManager struct {
	admission sync.Mutex
	prompts   []engine.PromptSnapshot
	mu        sync.Mutex
	job       Job
	cancel    context.CancelFunc
	done      chan struct{}
}

func (j *JobManager) Snapshot() Job {
	j.mu.Lock()
	defer j.mu.Unlock()
	copy := j.job
	copy.Trace = domain.CloneTrace(j.job.Trace)
	if j.job.Benchmark != nil {
		b := CloneBenchmarkReport(*j.job.Benchmark)
		copy.Benchmark = &b
	}
	if copy.Running {
		copy.DurationMS = time.Since(copy.Started).Milliseconds()
	}
	return copy
}
func (j *JobManager) Cancel() {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.cancel != nil {
		j.job.CancelRequested = true
		j.cancel()
	}
}

// StartPrepared registers the entire operation before capture. Cancelling the
// operation while GDI finishes discards the frame before any provider call.
func (j *JobManager) Start(parent context.Context, b liveBackend, pipeline string, input engine.Input) (string, error) {
	return j.StartPrepared(parent, b, pipeline, func(context.Context) (engine.Input, error) { return input, nil })
}
func (j *JobManager) StartPrepared(parent context.Context, b liveBackend, pipeline string, prepare func(context.Context) (engine.Input, error)) (string, error) {
	return j.startTask(parent, pipeline, func(ctx context.Context, opt engine.RunOptions) (engine.Result, *BenchmarkReport, error) {
		if ctx.Err() != nil {
			return engine.Result{}, nil, ctx.Err()
		}
		input, err := prepare(ctx)
		if err != nil {
			return engine.Result{}, nil, err
		}
		if ctx.Err() != nil {
			return engine.Result{}, nil, ctx.Err()
		}
		j.mu.Lock()
		j.job.Phase = "running"
		j.mu.Unlock()
		result, err := b.Run(ctx, pipeline, input, opt)
		return result, nil, err
	})
}
func (j *JobManager) startTask(parent context.Context, pipeline string, run func(context.Context, engine.RunOptions) (engine.Result, *BenchmarkReport, error)) (string, error) {
	return j.startTaskPurpose(parent, pipeline, "pipeline", run)
}
func (j *JobManager) startTaskPurpose(parent context.Context, pipeline, purpose string, run func(context.Context, engine.RunOptions) (engine.Result, *BenchmarkReport, error)) (string, error) {
	j.admission.Lock()
	defer j.admission.Unlock()
	j.mu.Lock()
	if j.job.Running {
		j.mu.Unlock()
		return "", fmt.Errorf("a run is already active; stop it first")
	}
	ctx, cancel := context.WithCancelCause(parent)
	id := fmt.Sprintf("run-%d", time.Now().UnixNano())
	done := make(chan struct{})
	j.job = Job{Purpose: purpose, ID: id, Running: true, Phase: "preparing", Started: time.Now(), Trace: domain.ExecutionTrace{PipelineID: pipeline, Events: []domain.TraceEvent{}}}
	j.prompts = nil
	j.cancel = func() { cancel(engine.ErrUserCancelled) }
	j.done = done
	j.mu.Unlock()
	go func() {
		defer cancel(nil)
		defer close(done)
		opt := engine.RunOptions{
			OnPrompt: func(p engine.PromptSnapshot) { j.rememberPrompt(id, p) },
			Purpose:  purpose,
			OnStart: func(tr domain.ExecutionTrace) {
				j.mu.Lock()
				defer j.mu.Unlock()
				if j.job.ID == id {
					j.job.Trace = domain.CloneTrace(tr)
				}
			},
			OnDelta: func(delta string) {
				j.mu.Lock()
				defer j.mu.Unlock()
				if j.job.ID == id && !j.job.Cleared {
					j.job.Text += delta
				}
			},
			OnReset: func() {
				j.mu.Lock()
				defer j.mu.Unlock()
				if j.job.ID == id {
					j.job.Text = ""
				}
			},
			OnTrace: func(ev domain.TraceEvent) {
				j.mu.Lock()
				defer j.mu.Unlock()
				if j.job.ID == id {
					j.job.Trace.Events = append(j.job.Trace.Events, domain.CloneTraceEvent(ev))
				}
			},
		}
		result, report, err := run(ctx, opt)
		j.mu.Lock()
		defer j.mu.Unlock()
		if j.job.ID != id {
			return
		}
		j.job.Running = false
		j.job.Phase = "finished"
		j.job.Trace = domain.CloneTrace(result.Trace)
		j.job.DurationMS = time.Since(j.job.Started).Milliseconds()
		j.job.Benchmark = report
		if !j.job.Cleared {
			if err != nil {
				j.job.Error = err.Error()
			} else {
				j.job.Text = result.Text
			}
		}
	}()
	return id, nil
}
func (j *JobManager) Clear() {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.cancel != nil {
		j.job.CancelRequested = true
		j.cancel()
	}
	j.prompts = nil
	j.job.Cleared = true
	j.job.Text = ""
	j.job.Error = ""
}
func (j *JobManager) Wait(ctx context.Context, id string) (Job, error) {
	j.mu.Lock()
	done := j.done
	current := j.job.ID
	j.mu.Unlock()
	if current != id {
		return Job{}, fmt.Errorf("run no longer current")
	}
	select {
	case <-done:
		return j.Snapshot(), nil
	case <-ctx.Done():
		return Job{}, ctx.Err()
	}
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
		id, err := s.jobs.StartPrepared(s.ctx, b, req.PipelineID, func(ctx context.Context) (engine.Input, error) {
			input := engine.Input{Text: req.Text}
			var err error
			switch req.Source {
			case "synthetic":
				input.ImageJPEG, err = benchmark.ImageJPEG()
			case "screen":
				if s.Capture == nil {
					err = fmt.Errorf("screen capture requires Windows")
				} else {
					input.ImageJPEG, err = s.Capture()
				}
			default:
				if req.Image != "" {
					input.ImageJPEG, err = base64.StdEncoding.DecodeString(req.Image)
				}
			}
			return input, err
		})
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

func (s *Server) StartScreen() (string, error) {
	b, ok := s.backend.(liveBackend)
	if !ok {
		return "", fmt.Errorf("live execution unavailable")
	}
	return s.jobs.StartPrepared(s.ctx, b, "", func(ctx context.Context) (engine.Input, error) {
		if s.Capture == nil {
			return engine.Input{}, fmt.Errorf("capture unavailable")
		}
		image, err := s.Capture()
		return engine.Input{ImageJPEG: image}, err
	})
}
func (s *Server) ClearRun() { s.jobs.Clear() }
