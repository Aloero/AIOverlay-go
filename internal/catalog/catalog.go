package catalog

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"aioverlay/internal/atomicfile"
	"aioverlay/internal/domain"
)

const LiveURL = "https://models.dev/api.json"

type Service struct {
	refreshMu     sync.Mutex
	lastRefresh   time.Time
	lastError     string
	mu            sync.RWMutex
	models        []domain.ModelMeta
	byRef         map[string]domain.ModelMeta
	client        *http.Client
	path          string
	opencodeReady map[string]bool
}

func New(path string) (*Service, error) {
	s := &Service{path: path, client: &http.Client{Timeout: 10 * time.Second}, opencodeReady: map[string]bool{}}
	s.loadOpenCodeReady()
	if err := s.LoadSnapshot(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Service) loadOpenCodeReady() {
	path := filepath.Join(filepath.Dir(s.path), "opencode-enabled.json")
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var data struct {
		Models []struct {
			Ref string `json:"ref"`
		} `json:"models"`
	}
	if json.Unmarshal(b, &data) != nil {
		return
	}
	for _, row := range data.Models {
		if row.Ref != "" {
			s.opencodeReady[row.Ref] = true
		}
	}
}

func (s *Service) LoadSnapshot() error {
	f, err := os.Open(s.path)
	if err != nil {
		return err
	}
	defer f.Close()
	models, err := parse(f)
	if err != nil {
		return err
	}
	s.replace(models)
	return nil
}

func (s *Service) Refresh() error { return s.RefreshContext(context.Background()) }
func (s *Service) RefreshContext(ctx context.Context) (err error) {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	defer func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if err != nil {
			s.lastError = err.Error()
		} else {
			s.lastError = ""
			s.lastRefresh = time.Now()
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, LiveURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "AIOverlay-go/0.2.6")
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("catalog download failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("catalog HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20+1))
	if err != nil || len(raw) > 32<<20 {
		return fmt.Errorf("catalog response unreadable or larger than 32 MiB")
	}
	models, err := parse(bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("catalog validation failed")
	}
	if len(models) == 0 {
		return fmt.Errorf("empty catalog response; existing snapshot retained")
	}
	// Save before publishing: failed writes preserve both disk and live snapshot.
	if err := atomicfile.Write(s.path, raw, 0o644); err != nil {
		return fmt.Errorf("catalog snapshot save: %w", err)
	}
	s.replace(models)
	return nil
}
func (s *Service) Status() map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return map[string]any{"models": len(s.models), "last_refresh": s.lastRefresh, "error": s.lastError}
}

func (s *Service) replace(models []domain.ModelMeta) {
	by := make(map[string]domain.ModelMeta, len(models))
	for _, m := range models {
		by[m.ProviderID+"/"+m.ModelID] = m
	}
	s.mu.Lock()
	s.models = models
	s.byRef = by
	s.mu.Unlock()
}

func (s *Service) Models() []domain.ModelMeta {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]domain.ModelMeta, len(s.models))
	copy(out, s.models)
	return out
}

func (s *Service) Find(ref string) (domain.ModelMeta, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, ok := s.byRef[strings.TrimSpace(ref)]
	return m, ok
}

type rawProvider struct {
	Name   string              `json:"name"`
	Env    []string            `json:"env"`
	Models map[string]rawModel `json:"models"`
}

type rawModel struct {
	Family      string `json:"family"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Reasoning   bool   `json:"reasoning"`
	Attachment  bool   `json:"attachment"`
	Modalities  struct {
		Input  []string `json:"input"`
		Output []string `json:"output"`
	} `json:"modalities"`
	Limit struct {
		Context int `json:"context"`
		Output  int `json:"output"`
	} `json:"limit"`
	Cost *struct {
		Input  *float64 `json:"input"`
		Output *float64 `json:"output"`
	} `json:"cost"`
}

func parse(r io.Reader) ([]domain.ModelMeta, error) {
	dec := json.NewDecoder(r)
	providers := map[string]rawProvider{}
	if err := dec.Decode(&providers); err != nil {
		return nil, fmt.Errorf("decode catalog: %w", err)
	}
	out := make([]domain.ModelMeta, 0, 8000)
	for pid, p := range providers {
		for mid, m := range p.Models {
			inputs := append([]string(nil), m.Modalities.Input...)
			outputs := append([]string(nil), m.Modalities.Output...)
			free := m.Cost != nil && m.Cost.Input != nil && m.Cost.Output != nil && *m.Cost.Input == 0 && *m.Cost.Output == 0
			var inputCost, outputCost float64
			if m.Cost != nil {
				if m.Cost.Input != nil {
					inputCost = *m.Cost.Input
				}
				if m.Cost.Output != nil {
					outputCost = *m.Cost.Output
				}
			}
			out = append(out, domain.ModelMeta{
				Family: m.Family, ProviderID: pid, ProviderName: p.Name, CredentialEnv: credentialEnvs(p.Env), ModelID: mid, ModelName: m.Name, Description: m.Description,
				InputModalities: inputs, OutputModalities: outputs, Reasoning: m.Reasoning,
				Streaming: true, Free: free, Context: m.Limit.Context, OutputLimit: m.Limit.Output,
				InputCost: inputCost, OutputCost: outputCost,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ProviderName == out[j].ProviderName {
			return out[i].ModelName < out[j].ModelName
		}
		return out[i].ProviderName < out[j].ProviderName
	})
	return out, nil
}
