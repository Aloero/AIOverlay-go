package control

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
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
	Finished        time.Time             `json:"finished,omitempty"`
	DurationMS      int64                 `json:"duration_ms"`
	QueuedID        string                `json:"queued_id,omitempty"`
	QueuedPurpose   string                `json:"queued_purpose,omitempty"`
}

type queuedJob struct {
	parent        context.Context
	pipeline      string
	purpose       string
	id            string
	run           func(context.Context, engine.RunOptions) (engine.Result, *BenchmarkReport, error)
	cancelPrepare context.CancelFunc
}

var jobSequence atomic.Uint64

type JobManager struct {
	admission sync.Mutex
	prompts   []engine.PromptSnapshot
	mu        sync.Mutex
	job       Job
	cancel    context.CancelFunc
	done      chan struct{}
	pending   *queuedJob
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
	return j.startTaskPurposeID(parent, pipeline, purpose, newJobID(), run)
}

func (j *JobManager) queueTaskPurpose(parent context.Context, pipeline, purpose string, prepareQueued func(context.Context) error, run func(context.Context, engine.RunOptions) (engine.Result, *BenchmarkReport, error)) (string, error) {
	j.admission.Lock()
	j.mu.Lock()
	if !j.job.Running {
		j.mu.Unlock()
		id, err := j.startTaskPurposeIDWithoutAdmission(parent, pipeline, purpose, newJobID(), run, nil)
		j.admission.Unlock()
		return id, err
	}
	if j.pending != nil {
		j.mu.Unlock()
		j.admission.Unlock()
		return "", fmt.Errorf("manual command queue is full; wait for or cancel the pending command")
	}
	id := newJobID()
	ready := make(chan struct{})
	prepareCtx, cancelPrepare := context.WithCancel(parent)
	var prepareErr error
	queuedRun := func(ctx context.Context, opt engine.RunOptions) (engine.Result, *BenchmarkReport, error) {
		select {
		case <-ready:
		case <-ctx.Done():
			return engine.Result{}, nil, ctx.Err()
		}
		if prepareErr != nil {
			return engine.Result{}, nil, prepareErr
		}
		return run(ctx, opt)
	}
	j.pending = &queuedJob{parent: parent, pipeline: pipeline, purpose: purpose, id: id, run: queuedRun, cancelPrepare: cancelPrepare}
	j.job.QueuedID = id
	j.job.QueuedPurpose = purpose
	j.mu.Unlock()
	j.admission.Unlock()

	if prepareQueued != nil {
		prepareErr = prepareQueued(prepareCtx)
	}
	close(ready)
	cancelPrepare()
	j.mu.Lock()
	accepted := (j.pending != nil && j.pending.id == id) || j.job.ID == id
	j.mu.Unlock()
	if !accepted {
		return "", context.Canceled
	}
	return id, nil
}

func newJobID() string {
	return fmt.Sprintf("run-%d-%d", time.Now().UnixNano(), jobSequence.Add(1))
}

func (j *JobManager) startTaskPurposeID(parent context.Context, pipeline, purpose, id string, run func(context.Context, engine.RunOptions) (engine.Result, *BenchmarkReport, error)) (string, error) {
	j.admission.Lock()
	defer j.admission.Unlock()
	return j.startTaskPurposeIDWithoutAdmission(parent, pipeline, purpose, id, run, nil)
}

func (j *JobManager) startTaskPurposeIDWithoutAdmission(parent context.Context, pipeline, purpose, id string, run func(context.Context, engine.RunOptions) (engine.Result, *BenchmarkReport, error), cancelPrepare context.CancelFunc) (string, error) {
	j.mu.Lock()
	if j.job.Running {
		j.mu.Unlock()
		return "", fmt.Errorf("a run is already active; stop it first")
	}
	ctx, cancel := context.WithCancelCause(parent)
	done := make(chan struct{})
	j.job = Job{Purpose: purpose, ID: id, Running: true, Phase: "preparing", Started: time.Now(), Trace: domain.ExecutionTrace{PipelineID: pipeline, Events: []domain.TraceEvent{}}}
	j.prompts = nil
	j.cancel = func() {
		cancel(engine.ErrUserCancelled)
		if cancelPrepare != nil {
			cancelPrepare()
		}
	}
	j.done = done
	j.mu.Unlock()
	go j.runTask(ctx, cancel, done, id, pipeline, purpose, run)
	return id, nil
}

func (j *JobManager) runTask(ctx context.Context, cancel context.CancelCauseFunc, done chan struct{}, id, pipeline, purpose string, run func(context.Context, engine.RunOptions) (engine.Result, *BenchmarkReport, error)) {
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
	j.finishTask(id, result, report, err)
}

func (j *JobManager) finishTask(id string, result engine.Result, report *BenchmarkReport, runErr error) {
	j.admission.Lock()
	defer j.admission.Unlock()
	j.mu.Lock()
	if j.job.ID != id {
		j.mu.Unlock()
		return
	}
	j.job.Running = false
	j.job.Finished = time.Now()
	j.job.Phase = "finished"
	j.job.Trace = domain.CloneTrace(result.Trace)
	j.job.DurationMS = time.Since(j.job.Started).Milliseconds()
	j.job.Benchmark = report
	if !j.job.Cleared {
		if runErr != nil {
			j.job.Error = runErr.Error()
		} else {
			j.job.Text = result.Text
		}
	}
	pending := j.pending
	j.pending = nil
	if pending == nil {
		j.job.QueuedID = ""
		j.job.QueuedPurpose = ""
		j.mu.Unlock()
		return
	}
	j.mu.Unlock()
	_, _ = j.startTaskPurposeIDWithoutAdmission(pending.parent, pending.pipeline, pending.purpose, pending.id, pending.run, pending.cancelPrepare)
}

func (j *JobManager) Clear() {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.cancel != nil {
		j.job.CancelRequested = true
		j.cancel()
	}
	j.prompts = nil
	if j.pending != nil && j.pending.cancelPrepare != nil {
		j.pending.cancelPrepare()
	}
	j.pending = nil
	j.job.QueuedID = ""
	j.job.QueuedPurpose = ""
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
		job := j.Snapshot()
		if job.ID != id {
			return Job{}, fmt.Errorf("run completed and a queued run is now current")
		}
		return job, nil
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

func (s *Server) StartScreenLegacy() (string, error) {
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

func (s *Server) StartScreen() (string, error) { return s.StartCommand("analyze") }
