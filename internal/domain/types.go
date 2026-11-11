package domain

import (
	"encoding/json"
	"time"
)

const ConfigVersion = 7

type ProviderKind string

const (
	ProviderOpenAICompatible ProviderKind = "openai_compatible"
	ProviderGemini           ProviderKind = "gemini"
	ProviderTranscription    ProviderKind = "audio_transcription"
	ProviderEmbeddedOpenCode ProviderKind = "embedded_opencode"
	ProviderEmbeddedHermes   ProviderKind = "embedded_hermes"
	ProviderOpenAILive       ProviderKind = "openai_live"
	ProviderLocalSTT         ProviderKind = "local_stt"
)

// DeliveryApp is the application (harness) through which a provider is registered and used.
// "direct" means the app's own protocol adapters; an external app owns the provider
// registration and interprets that model's parameters — the app deliberately does not
// replicate per-harness parameter logic.
type DeliveryApp string

const (
	AppDirect   DeliveryApp = "direct"
	AppOpenCode DeliveryApp = "opencode"
	AppHermes   DeliveryApp = "hermes"
)

func ValidDeliveryApp(a DeliveryApp) bool { return enum(a, AppDirect, AppOpenCode, AppHermes) }

// enum lives in the config package's terms; domain keeps its own copy for the checks above.
func enum(v DeliveryApp, values ...DeliveryApp) bool {
	for _, x := range values {
		if v == x {
			return true
		}
	}
	return false
}

type CredentialPolicy string

const (
	CredentialRoundRobin CredentialPolicy = "round_robin"
	CredentialSticky     CredentialPolicy = "sticky_last_good"
	CredentialSequential CredentialPolicy = "sequential"
	CredentialRandom     CredentialPolicy = "random_healthy"
)

type PoolPolicy string

const (
	PoolRoundRobin PoolPolicy = "round_robin"
	PoolPriority   PoolPolicy = "priority"
	PoolStaggered  PoolPolicy = "staggered_race"
	PoolParallel   PoolPolicy = "parallel_race"
	PoolFastest    PoolPolicy = "fastest_healthy"
	PoolWeighted   PoolPolicy = "weighted"
)

type NodeType string

const (
	NodeScreen    NodeType = "screen"
	NodeAudio     NodeType = "audio"
	NodeContext   NodeType = "context"
	NodeDecision  NodeType = "decision"
	NodeCapture   NodeType = "capture_screen"
	NodeModelPool NodeType = "model_pool"
	NodeValidator NodeType = "validator"
	NodeOutput    NodeType = "output"
)

type Outcome string

const (
	OutcomeSuccess             Outcome = "success"
	OutcomeAnyError            Outcome = "any_error"
	OutcomeAuthFailed          Outcome = "auth_failed"
	OutcomeRateLimited         Outcome = "rate_limited"
	OutcomeQuotaExceeded       Outcome = "quota_exceeded"
	OutcomeTimeout             Outcome = "timeout"
	OutcomeStreamIdleTimeout   Outcome = "stream_idle_timeout"
	OutcomeFirstTokenTimeout   Outcome = "first_token_timeout"
	OutcomeNetworkError        Outcome = "network_error"
	OutcomeModelUnavailable    Outcome = "model_unavailable"
	OutcomeRegionRestricted    Outcome = "region_restricted"
	OutcomeUnsupportedModality Outcome = "unsupported_modality"
	OutcomeProviderUnavailable Outcome = "provider_unavailable"
	OutcomeInvalidResponse     Outcome = "invalid_response"
	OutcomeValidationFailed    Outcome = "validation_failed"
	OutcomeCancelled           Outcome = "cancelled"
)

type Config struct {
	RuntimeStatistics *RuntimeStatistics         `json:"runtime_statistics,omitempty"`
	Extra             map[string]json.RawMessage `json:"-"`
	Version           int                        `json:"version"`
	App               AppConfig                  `json:"app"`
	Providers         []ProviderConfig           `json:"providers"`
	Pipelines         []Pipeline                 `json:"pipelines"`
	ActivePipelineID  string                     `json:"active_pipeline_id"`
	Overlay           OverlayConfig              `json:"overlay"`
	ControlCenter     ControlConfig              `json:"control_center"`
	VPN               VPNConfig                  `json:"vpn,omitempty"`
	Build             BuildProfile               `json:"build"`
}

type AppConfig struct {
	Extra               map[string]json.RawMessage `json:"-"`
	Name                string                     `json:"name"`
	SecretStorage       string                     `json:"secret_storage"`
	PersistRuntimeStats bool                       `json:"persist_runtime_stats"`
	CatalogRefreshHours int                        `json:"catalog_refresh_hours"`
}

type ProviderConfig struct {
	AudioInput        bool                       `json:"audio_input,omitempty"`
	Extra             map[string]json.RawMessage `json:"-"`
	CatalogProviderID string                     `json:"catalog_provider_id,omitempty"`
	ID                string                     `json:"id"`
	Label             string                     `json:"label"`
	Kind              ProviderKind               `json:"kind"`
	// App records which application fronts this provider. Empty means "derived from Kind" and
	// is normalized by migration; DeliveryApp() is the accessor to use.
	App                DeliveryApp             `json:"app,omitempty"`
	Enabled            bool                    `json:"enabled"`
	BaseURL            string                  `json:"base_url,omitempty"`
	ModelsURL          string                  `json:"models_url,omitempty"`
	OpenCodeProviderID string                  `json:"opencode_provider_id,omitempty"`
	CredentialEnv      []string                `json:"credential_env,omitempty"`
	Headers            map[string]string       `json:"headers,omitempty"`
	CredentialPool     CredentialPool          `json:"credential_pool"`
	RequestDefaults    RequestDefaults         `json:"request_defaults"`
	Realtime           *RealtimeProviderConfig `json:"realtime,omitempty"`
	LocalSTT           *LocalSTTProviderConfig `json:"local_stt,omitempty"`
}

type LocalSTTProviderConfig struct {
	Extra          map[string]json.RawMessage `json:"-"`
	HelperPath     string                     `json:"helper_path"`
	ModelPath      string                     `json:"model_path"`
	ModelID        string                     `json:"model_id"`
	Args           []string                   `json:"args,omitempty"`
	ResourcePolicy string                     `json:"resource_policy,omitempty"`
}

type RealtimeProviderConfig struct {
	Extra          map[string]json.RawMessage `json:"-"`
	ResponsesModel string                     `json:"responses_model,omitempty"`
	Instructions   string                     `json:"instructions,omitempty"`
	Store          bool                       `json:"store,omitempty"`
}

type RequestDefaults struct {
	Extra               map[string]json.RawMessage `json:"-"`
	ConnectTimeoutMS    int                        `json:"connect_timeout_ms"`
	FirstTokenTimeoutMS int                        `json:"first_token_timeout_ms"`
	ResponseTimeoutMS   int                        `json:"response_timeout_ms"`
	StreamIdleTimeoutMS int                        `json:"stream_idle_timeout_ms"`
	MaxOutputTokens     int                        `json:"max_output_tokens"`
}

type CredentialPool struct {
	Extra              map[string]json.RawMessage `json:"-"`
	Policy             CredentialPolicy           `json:"policy"`
	Keys               []Credential               `json:"keys"`
	MaxKeysPerRequest  int                        `json:"max_keys_per_request"`
	RateLimitCooldownS int                        `json:"rate_limit_cooldown_s"`
}

type Credential struct {
	Extra    map[string]json.RawMessage `json:"-"`
	ID       string                     `json:"id"`
	Label    string                     `json:"label"`
	Secret   string                     `json:"secret"`
	Enabled  bool                       `json:"enabled"`
	Priority int                        `json:"priority"`
}

type Pipeline struct {
	Commands      []CommandConfig            `json:"commands,omitempty"`
	Sources       []EventSource              `json:"sources,omitempty"`
	Contexts      []ContextConfig            `json:"contexts,omitempty"`
	Context       *ConversationConfig        `json:"context,omitempty"` // schema-4 migration only
	SessionBudget *SessionBudget             `json:"session_budget,omitempty"`
	Extra         map[string]json.RawMessage `json:"-"`
	ID            string                     `json:"id"`
	Name          string                     `json:"name"`
	Description   string                     `json:"description,omitempty"`
	Enabled       bool                       `json:"enabled"`
	EntryNodeID   string                     `json:"entry_node_id"`
	OutputNodeID  string                     `json:"output_node_id"`
	Budget        ExecutionBudget            `json:"budget"`
	Nodes         []Node                     `json:"nodes"`
	Edges         []Edge                     `json:"edges"`
}

type ExecutionBudget struct {
	Extra               map[string]json.RawMessage `json:"-"`
	MaxWallTimeMS       int                        `json:"max_wall_time_ms"`
	MaxModelCalls       int                        `json:"max_model_calls"`
	MaxParallelCalls    int                        `json:"max_parallel_calls"`
	MaxProviders        int                        `json:"max_providers"`
	MaxKeysPerProvider  int                        `json:"max_keys_per_provider"`
	ConnectTimeoutMS    int                        `json:"connect_timeout_ms"`
	FirstTokenTimeoutMS int                        `json:"first_token_timeout_ms"`
	ResponseTimeoutMS   int                        `json:"response_timeout_ms"`
	StreamIdleTimeoutMS int                        `json:"stream_idle_timeout_ms"`
}

type Position struct {
	Extra map[string]json.RawMessage `json:"-"`
	X     float64                    `json:"x"`
	Y     float64                    `json:"y"`
}

type Node struct {
	Extra       map[string]json.RawMessage `json:"-"`
	ID          string                     `json:"id"`
	Type        NodeType                   `json:"type"`
	Name        string                     `json:"name"`
	Position    Position                   `json:"position"`
	ContextMode string                     `json:"context_mode,omitempty"`
	ContextID   string                     `json:"context_id,omitempty"`
	ModelPool   *ModelPoolConfig           `json:"model_pool,omitempty"`
	Validator   *ValidatorConfig           `json:"validator,omitempty"`
}

// CatalogSelection is an explicit model source, independent of dispatch policy.
type CatalogSelection struct {
	Extra               map[string]json.RawMessage `json:"-"`
	Mode                string                     `json:"mode"`
	ProviderIDs         []string                   `json:"provider_ids,omitempty"`
	FreeOnly            bool                       `json:"free_only"`
	Modality            string                     `json:"modality"`
	DefaultQualityScore float64                    `json:"default_quality_score"`
}

type ModelPoolConfig struct {
	Task                       string                     `json:"task,omitempty"`
	ResultName                 string                     `json:"result_name,omitempty"`
	Order                      PoolPolicy                 `json:"order,omitempty"`
	Dispatch                   DispatchPolicy             `json:"dispatch,omitempty"`
	Extra                      map[string]json.RawMessage `json:"-"`
	Selection                  *CatalogSelection          `json:"selection,omitempty"`
	Policy                     PoolPolicy                 `json:"policy"`
	Models                     []ModelSlot                `json:"models"`
	VisionPreprocessor         *ModelPoolConfig           `json:"vision_preprocessor,omitempty"`
	AudioPreprocessor          *ModelPoolConfig           `json:"audio_preprocessor,omitempty"`
	VisionPreprocessorOptional bool                       `json:"vision_preprocessor_optional,omitempty"`
	AudioPreprocessorOptional  bool                       `json:"audio_preprocessor_optional,omitempty"`
	Prompt                     PromptConfig               `json:"prompt"`
	QualityFloor               float64                    `json:"quality_floor"`
	MaxParallel                int                        `json:"max_parallel"`
	MaxCalls                   int                        `json:"max_calls"`
	HedgeDelayMS               int                        `json:"hedge_delay_ms"`
	RoundRobinCursor           int                        `json:"round_robin_cursor"`
}

type ModelSlot struct {
	Extra      map[string]json.RawMessage `json:"-"`
	ID         string                     `json:"id"`
	ProviderID string                     `json:"provider_id"`
	ModelID    string                     `json:"model_id"`
	// AppParams are model parameters owned by the delivery application (reasoning effort,
	// thinking mode, …). The app forwards them untouched; only the application interprets them.
	AppParams           map[string]json.RawMessage `json:"app_params,omitempty"`
	Enabled             bool                       `json:"enabled"`
	Priority            int                        `json:"priority"`
	Weight              float64                    `json:"weight"`
	QualityScore        float64                    `json:"quality_score"`
	TimeoutMS           int                        `json:"timeout_ms"`
	FirstTokenTimeoutMS int                        `json:"first_token_timeout_ms"`
	MaxAttempts         int                        `json:"max_attempts"`
}

type PromptConfig struct {
	Extra    map[string]json.RawMessage `json:"-"`
	System   string                     `json:"system"`
	User     string                     `json:"user"`
	Template string                     `json:"template,omitempty"`
}

type ValidatorConfig struct {
	Extra     map[string]json.RawMessage `json:"-"`
	Kind      string                     `json:"kind"`
	Pattern   string                     `json:"pattern,omitempty"`
	MinLength int                        `json:"min_length,omitempty"`
	MaxLength int                        `json:"max_length,omitempty"`
}

type Edge struct {
	Extra     map[string]json.RawMessage `json:"-"`
	ID        string                     `json:"id"`
	From      string                     `json:"from"`
	To        string                     `json:"to"`
	On        []Outcome                  `json:"on"`
	Priority  int                        `json:"priority"`
	DelayMS   int                        `json:"delay_ms,omitempty"`
	MaxVisits int                        `json:"max_visits"`
}

type OverlayConfig struct {
	X            int                        `json:"x,omitempty"`
	Y            int                        `json:"y,omitempty"`
	Positioned   bool                       `json:"positioned,omitempty"`
	Extra        map[string]json.RawMessage `json:"-"`
	Opacity      float64                    `json:"opacity"`
	Width        int                        `json:"width"`
	Height       int                        `json:"height"`
	FontSize     int                        `json:"font_size"`
	Stealth      bool                       `json:"stealth"`
	ClickThrough bool                       `json:"click_through"`
	StationaryMS int                        `json:"stationary_cursor_ms"`
	Hotkeys      map[string]string          `json:"hotkeys"`
}

type CanvasLayout struct {
	Extra map[string]json.RawMessage `json:"-"`
	PanX  float64                    `json:"pan_x"`
	PanY  float64                    `json:"pan_y"`
	Zoom  float64                    `json:"zoom"`
}

type ControlConfig struct {
	Workspace      WorkspaceConfig            `json:"workspace,omitempty"`
	FavoriteModels []string                   `json:"favorite_models,omitempty"`
	RecentModels   []string                   `json:"recent_models,omitempty"`
	Extra          map[string]json.RawMessage `json:"-"`
	Layout         map[string]CanvasLayout    `json:"layout,omitempty"`
	ListenAddress  string                     `json:"listen_address"`
	AutoOpen       bool                       `json:"auto_open"`
	Theme          string                     `json:"theme"`
}

type VPNConfig struct {
	Name    string                     `json:"name,omitempty"`
	Extra   map[string]json.RawMessage `json:"-"`
	Enabled bool                       `json:"enabled"`
	Engine  string                     `json:"engine,omitempty"`
	Config  json.RawMessage            `json:"config,omitempty"`
}

type BuildProfile struct {
	Extra           map[string]json.RawMessage `json:"-"`
	ExecutableName  string                     `json:"executable_name"`
	ProductName     string                     `json:"product_name"`
	FileDescription string                     `json:"file_description"`
	Company         string                     `json:"company"`
	Version         string                     `json:"version"`
	IconPath        string                     `json:"icon_path"`
}

type ProviderMeta struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	CredentialEnv []string `json:"credential_env"`
	Models        int      `json:"models"`
}

type ModelMeta struct {
	Family           string   `json:"family,omitempty"`
	ProviderID       string   `json:"provider_id"`
	ProviderName     string   `json:"provider_name"`
	CredentialEnv    []string `json:"credential_env,omitempty"`
	ModelID          string   `json:"model_id"`
	ModelName        string   `json:"model_name"`
	Description      string   `json:"description,omitempty"`
	InputModalities  []string `json:"input_modalities"`
	OutputModalities []string `json:"output_modalities"`
	Reasoning        bool     `json:"reasoning"`
	Streaming        bool     `json:"streaming"`
	Free             bool     `json:"free"`
	Context          int      `json:"context"`
	OutputLimit      int      `json:"output_limit"`
	InputCost        float64  `json:"input_cost"`
	OutputCost       float64  `json:"output_cost"`
	Ready            bool     `json:"ready"`
	Status           string   `json:"status"`
	// Evidence is the strongest layer that asserted readiness; Blockers explains why it is not ready.
	// "ready: false" without them could not distinguish an unconfigured connection from a model the
	// runtime refused a minute ago, which is the difference the user acts on.
	Evidence Provenance `json:"evidence,omitempty"`
	Blockers []Blocker  `json:"blockers,omitempty"`
}

func (m ModelMeta) HasImageInput() bool {
	for _, v := range m.InputModalities {
		if v == "image" {
			return true
		}
	}
	return false
}

func (m ModelMeta) TextOnly() bool { return !m.HasImageInput() }

type ExecutionTrace struct {
	SourceID       string          `json:"source_id,omitempty"`
	EventID        string          `json:"event_id,omitempty"`
	Intent         string          `json:"intent,omitempty"`
	Outcome        Outcome         `json:"outcome,omitempty"`
	Error          string          `json:"error,omitempty"`
	FailureNodeID  string          `json:"failure_node_id,omitempty"`
	CancelReason   string          `json:"cancel_reason,omitempty"`
	PipelineName   string          `json:"pipeline_name,omitempty"`
	ConfigRevision uint64          `json:"config_revision,omitempty"`
	Budget         ExecutionBudget `json:"budget"`
	Purpose        string          `json:"purpose,omitempty"`
	ID             string          `json:"id"`
	PipelineID     string          `json:"pipeline_id"`
	StartedAt      time.Time       `json:"started_at"`
	FinishedAt     time.Time       `json:"finished_at,omitempty"`
	Winner         *AttemptRef     `json:"winner,omitempty"`
	Events         []TraceEvent    `json:"events"`
}

type AttemptRef struct {
	SlotID          string `json:"slot_id,omitempty"`
	ID              string `json:"id,omitempty"`
	ProviderID      string `json:"provider_id"`
	ModelID         string `json:"model_id"`
	CredentialID    string `json:"credential_id,omitempty"`
	CredentialAlias string `json:"credential_alias,omitempty"`
	Transport       string `json:"transport,omitempty"`
}

type TraceEvent struct {
	PlanID          string      `json:"plan_id,omitempty"`
	EdgeID          string      `json:"edge_id,omitempty"`
	BudgetScope     string      `json:"budget_scope,omitempty"`
	CancelReason    string      `json:"cancel_reason,omitempty"`
	CandidateCount  int         `json:"candidate_count,omitempty"`
	AttemptedModels int         `json:"attempted_models,omitempty"`
	UntriedModels   int         `json:"untried_models,omitempty"`
	AtMS            int64       `json:"at_ms"`
	Kind            string      `json:"kind"`
	NodeID          string      `json:"node_id,omitempty"`
	Attempt         *AttemptRef `json:"attempt,omitempty"`
	Outcome         Outcome     `json:"outcome,omitempty"`
	Message         string      `json:"message,omitempty"`
	TTFTMS          int64       `json:"ttft_ms,omitempty"`
	DurationMS      int64       `json:"duration_ms,omitempty"`
	InputTokens     int         `json:"input_tokens,omitempty"`
	OutputTokens    int         `json:"output_tokens,omitempty"`
	TPS             float64     `json:"tps,omitempty"`
	HTTPStatus      int         `json:"http_status,omitempty"`
}
