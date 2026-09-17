package control

import (
	"aioverlay/internal/config"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"time"

	"aioverlay/internal/domain"
	"aioverlay/internal/engine"
	webassets "aioverlay/web"
)

type CatalogQuery struct {
	ProviderID string
	Search     string
	Provider   string
	Vision     bool
	Free       bool
	Reasoning  bool
	Ready      bool
	Limit      int
	Offset     int
}

type CatalogPage struct {
	Items  []domain.ModelMeta `json:"items"`
	Total  int                `json:"total"`
	Offset int                `json:"offset"`
	Limit  int                `json:"limit"`
}
type BenchmarkRequest struct {
	PipelineID string `json:"pipeline_id"`
	Runs       int    `json:"runs"`
}
type BenchmarkRun struct {
	Verdict    string             `json:"verdict"`
	TTFTMS     int64              `json:"ttft_ms"`
	TPS        float64            `json:"tps"`
	Run        int                `json:"run"`
	DurationMS int64              `json:"duration_ms"`
	Correct    bool               `json:"correct"`
	Text       string             `json:"text"`
	Winner     *domain.AttemptRef `json:"winner,omitempty"`
}
type BenchmarkReport struct {
	Requested   int            `json:"requested"`
	PipelineID  string         `json:"pipeline_id"`
	Successful  int            `json:"successful"`
	Unverified  int            `json:"unverified"`
	SuccessRate float64        `json:"success_rate"`
	Cancelled   bool           `json:"cancelled"`
	CheckMethod string         `json:"check_method"`
	Runs        []BenchmarkRun `json:"runs"`
	MedianMS    int64          `json:"median_ms"`
	Correct     int            `json:"correct"`
	Total       int            `json:"total"`
}

type TestRequest struct {
	PipelineID  string `json:"pipeline_id"`
	ImageBase64 string `json:"image_base64"`
	Text        string `json:"text"`
}

type Backend interface {
	GetConfig() domain.Config
	SaveConfig(domain.Config) error
	Catalog(CatalogQuery) (CatalogPage, error)
	RefreshCatalog() error
	RunTest(context.Context, TestRequest) (engine.Result, error)
	RunBenchmark(context.Context, BenchmarkRequest) (BenchmarkReport, error)
	Traces() []domain.ExecutionTrace
}

type Server struct {
	ctx          context.Context
	jobs         JobManager
	Capture      func() ([]byte, error)
	backend      Backend
	http         *http.Server
	core         http.Handler
	token        string
	HotkeyEditor func(bool)
	Diagnostics  func() any
}

func New(addr string, b Backend) *Server {
	s := &Server{backend: b, ctx: context.Background()}
	mux := http.NewServeMux()
	s.routes(mux)
	s.core = securityHeaders(mux)
	s.token = newDevelopmentToken()
	s.http = &http.Server{Addr: addr, Handler: s.secureHTTP(s.core), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10}
	return s
}
func (s *Server) SetContext(ctx context.Context) { s.ctx = ctx }
func (s *Server) Handler() http.Handler          { return s.http.Handler }
func (s *Server) DesktopHandler() http.Handler   { return s.core }
func (s *Server) DevelopmentToken() string       { return s.token }
func (s *Server) SetDevelopmentToken(token string) {
	if len(token) >= 32 {
		s.token = token
	}
}
func (s *Server) CancelRun()   { s.jobs.Cancel() }
func (s *Server) Addr() string { return s.http.Addr }
func (s *Server) ListenAndServe() error {
	if err := validateListenAddress(s.http.Addr); err != nil {
		return err
	}
	return s.http.ListenAndServe()
}
func (s *Server) Shutdown(ctx context.Context) error { return s.http.Shutdown(ctx) }

func (s *Server) routes(mux *http.ServeMux) {
	mux.HandleFunc("/api/providers/config", s.handleProviderConfig)
	mux.HandleFunc("/api/providers/catalog", s.handleProviderDirectory)
	mux.HandleFunc("/api/pool/plan", s.handlePoolPlan)
	mux.HandleFunc("/api/pipelines/active", s.handleActivePipeline)
	static, _ := fs.Sub(webassets.FS, "static")
	mux.Handle("/assets/", http.StripPrefix("/assets/", http.FileServer(http.FS(static))))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		data, err := fs.ReadFile(static, "index.html")
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(data)
	})
	mux.HandleFunc("/api/prompts/preview", s.handlePromptPreview)
	mux.HandleFunc("/api/job", s.handleJob)
	mux.HandleFunc("/api/diagnostics", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			w.WriteHeader(405)
			return
		}
		if s.Diagnostics != nil {
			writeJSON(w, s.Diagnostics())
		} else {
			writeJSON(w, map[string]any{})
		}
	})
	mux.HandleFunc("/api/hotkeys/editor", s.handleHotkeyEditor)
	mux.HandleFunc("/api/capture", s.handleCapture)
	mux.HandleFunc("/api/config", s.handleConfig)
	mux.HandleFunc("/api/config/export", s.handleExport)
	mux.HandleFunc("/api/config/import", s.handleImport)
	mux.HandleFunc("/api/catalog", s.handleCatalog)
	mux.HandleFunc("/api/catalog/refresh", s.handleCatalogRefresh)
	mux.HandleFunc("/api/run", s.handleRun)
	mux.HandleFunc("/api/benchmark", s.handleBenchmark)
	mux.HandleFunc("/api/traces", s.handleTraces)
	mux.HandleFunc("/api/vpn/check", s.handleVPNCheck)
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, map[string]any{"ok": true}) })
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, s.backend.GetConfig())
	case http.MethodPut:
		var cfg domain.Config
		if err := decodeJSON(r, &cfg, 8<<20); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if err := s.backend.SaveConfig(cfg); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		writeJSON(w, map[string]any{"ok": true, "version": s.backend.GetConfig().Version})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}
func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", "attachment; filename=AIOverlay-config.json")
	json.NewEncoder(w).Encode(s.backend.GetConfig())
}
func (s *Server) handleImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(405)
		return
	}
	var cfg domain.Config
	if err := decodeJSON(r, &cfg, 8<<20); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if err := s.backend.SaveConfig(cfg); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}
func (s *Server) handleCatalog(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	page, err := s.backend.Catalog(CatalogQuery{ProviderID: q.Get("provider_id"), Search: q.Get("q"), Provider: q.Get("provider"), Vision: boolQ(q.Get("vision")), Free: boolQ(q.Get("free")), Reasoning: boolQ(q.Get("reasoning")), Ready: boolQ(q.Get("ready")), Limit: limit, Offset: offset})
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, page)
}
func (s *Server) handleCatalogRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(405)
		return
	}
	var err error
	if b, ok := s.backend.(interface{ RefreshCatalogContext(context.Context) error }); ok {
		err = b.RefreshCatalogContext(r.Context())
	} else {
		err = s.backend.RefreshCatalog()
	}
	if err != nil {
		http.Error(w, err.Error(), 502)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}
func (s *Server) handleRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(405)
		return
	}
	var req TestRequest
	if err := decodeJSON(r, &req, 20<<20); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if req.ImageBase64 != "" {
		if strings.Contains(req.ImageBase64, ",") {
			req.ImageBase64 = strings.SplitN(req.ImageBase64, ",", 2)[1]
		}
		if _, err := base64.StdEncoding.DecodeString(req.ImageBase64); err != nil {
			http.Error(w, "invalid image_base64", 400)
			return
		}
	}
	b, ok := s.backend.(liveBackend)
	if !ok {
		http.Error(w, "live execution unavailable", 501)
		return
	}
	input := engine.Input{Text: req.Text}
	if req.ImageBase64 != "" {
		input.ImageJPEG, _ = base64.StdEncoding.DecodeString(req.ImageBase64)
	}
	id, err := s.jobs.Start(r.Context(), b, req.PipelineID, input)
	if err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	job, err := s.jobs.Wait(r.Context(), id)
	if err != nil {
		http.Error(w, "run cancelled", 408)
		return
	}
	if job.Error != "" {
		http.Error(w, job.Error, 502)
		return
	}
	writeJSON(w, engine.Result{Text: job.Text, Trace: job.Trace})
}

func (s *Server) handleBenchmark(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req BenchmarkRequest
	if err := decodeJSON(r, &req, 1<<20); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if req.Runs <= 0 {
		req.Runs = 3
	}
	if req.Runs > 10 {
		req.Runs = 10
	}
	id, err := s.jobs.startTask(s.ctx, req.PipelineID, func(ctx context.Context, opt engine.RunOptions) (engine.Result, *BenchmarkReport, error) {
		return s.runBenchmark(ctx, req, opt)
	})
	if err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	writeJSON(w, map[string]string{"id": id})
}

func (s *Server) handleTraces(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.backend.Traces())
}
func boolQ(s string) bool { return s == "1" || strings.EqualFold(s, "true") }
func decodeJSON(r *http.Request, v any, max int64) error {
	defer r.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(r.Body, max+1))
	if err != nil {
		return fmt.Errorf("could not read JSON request")
	}
	if int64(len(raw)) > max {
		return fmt.Errorf("JSON request exceeds size limit")
	}
	if err := config.CheckJSON(raw); err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return fmt.Errorf("invalid JSON request fields")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		fmt.Fprint(w, `{"error":"encode"}`)
	}
}
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self'; img-src 'self' data:; connect-src 'self'")
		next.ServeHTTP(w, r)
	})
}
