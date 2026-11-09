// Package contracts contains transport-neutral application requests and reports.
package contracts

import (
	"aioverlay/internal/domain"
	"aioverlay/internal/engine"
)

type CatalogQuery struct {
	ProviderID     string   `json:"provider_id"`
	Search         string   `json:"search"`
	Provider       string   `json:"provider"`
	Vision         bool     `json:"vision"`
	Audio          bool     `json:"audio"`
	Free           bool     `json:"free"`
	Reasoning      bool     `json:"reasoning"`
	Ready          bool     `json:"ready"`
	Limit          int      `json:"limit"`
	Offset         int      `json:"offset"`
	References     []string `json:"references,omitempty"`
	ReferencesOnly bool     `json:"references_only,omitempty"`
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

type StudioState struct {
	RecentModels     []string                 `json:"recent_models"`
	ActivePipelineID string                   `json:"active_pipeline_id"`
	Revision         uint64                   `json:"revision"`
	Credentials      []engine.CredentialState `json:"credentials"`
	Version          string                   `json:"version"`
}
