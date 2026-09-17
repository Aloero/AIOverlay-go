package domain

import (
	"encoding/json"
	"time"
)

type CommandIntent string

const (
	IntentRespond CommandIntent = "respond"
	IntentDecide  CommandIntent = "decide"
	IntentContext CommandIntent = "context"
)

type SourceKind string

const (
	SourceAudio       SourceKind = "audio"
	SourceScreenTimer SourceKind = "screen_timer"
)

// CommandConfig is a user action. Commands are separate from continuously
// running sources so the legacy screenshot path does not require a session.
type CommandConfig struct {
	Extra               map[string]json.RawMessage `json:"-"`
	ID                  string                     `json:"id"`
	Name                string                     `json:"name"`
	Action              string                     `json:"action"` // analyze, screen_context, screen_decide
	EntryNodeID         string                     `json:"entry_node_id"`
	Intent              CommandIntent              `json:"intent"`
	ContextID           string                     `json:"context_id,omitempty"`
	CaptureScreen       bool                       `json:"capture_screen,omitempty"`
	WaitForTranscriptMS int                        `json:"wait_for_transcript_ms,omitempty"`
}

// EventSource is an independently scheduled source. Enabled controls whether the
// source belongs to the saved pipeline; AutoStart controls whether it starts with
// the application. Runtime pause/resume state is intentionally not serialized here.
type EventSource struct {
	Extra       map[string]json.RawMessage `json:"-"`
	ID          string                     `json:"id"`
	Name        string                     `json:"name"`
	Kind        SourceKind                 `json:"kind"`
	Enabled     bool                       `json:"enabled"`
	AutoStart   bool                       `json:"auto_start"`
	EntryNodeID string                     `json:"entry_node_id"`
	Intent      CommandIntent              `json:"intent"`
	ContextID   string                     `json:"context_id,omitempty"`
	IntervalMS  int                        `json:"interval_ms,omitempty"`
	ChangedOnly bool                       `json:"changed_only,omitempty"`
	DebounceMS  int                        `json:"debounce_ms,omitempty"`
	MaxWaitMS   int                        `json:"max_wait_ms,omitempty"`
	Audio       *AudioSourceConfig         `json:"audio,omitempty"`

	// Command is a schema-4 compatibility field. Schema 5 materializes manual
	// commands into Pipeline.Commands and leaves this empty.
	Command string `json:"command,omitempty"`
}

type AudioSourceConfig struct {
	Extra      map[string]json.RawMessage `json:"-"`
	Mode       string                     `json:"mode"`                  // system, microphone
	DeviceID   string                     `json:"device_id,omitempty"`   // empty = current default endpoint
	DeviceRole string                     `json:"device_role,omitempty"` // multimedia, communications
	SegmentMS  int                        `json:"segment_ms"`
	SilenceMS  int                        `json:"silence_ms"`
	MinimumMS  int                        `json:"minimum_ms"`
	Threshold  float64                    `json:"threshold"` // RMS gate; not semantic speech detection
}

// ContextConfig describes local in-memory context. It does not imply that a
// realtime provider session has forgotten or learned the same data.
type ContextConfig struct {
	Extra             map[string]json.RawMessage `json:"-"`
	ID                string                     `json:"id"`
	Name              string                     `json:"name"`
	Enabled           bool                       `json:"enabled"`
	MaxAgeS           int                        `json:"max_age_s"`
	MaxEntries        int                        `json:"max_entries"`
	MaxChars          int                        `json:"max_chars"`
	IncludeTranscript bool                       `json:"include_transcript"`
	IncludeAnswers    bool                       `json:"include_answers"`
	KeepLatestScreen  bool                       `json:"keep_latest_screen"`
	ScreenMaxAgeS     int                        `json:"screen_max_age_s"`
}

// ConversationConfig is retained only to decode schema-4 WIP data. New schema-5
// configuration uses ContextConfig in Pipeline.Contexts.
type ConversationConfig struct {
	Extra             map[string]json.RawMessage `json:"-"`
	Enabled           bool                       `json:"enabled"`
	MaxAgeS           int                        `json:"max_age_s"`
	MaxEntries        int                        `json:"max_entries"`
	MaxChars          int                        `json:"max_chars"`
	IncludeAnswers    bool                       `json:"include_answers"`
	KeepLatestScreen  bool                       `json:"keep_latest_screen"`
	ScreenMaxAgeS     int                        `json:"screen_max_age_s"`
	ManualUsesContext bool                       `json:"manual_uses_context"`
}

type SessionBudget struct {
	Extra            map[string]json.RawMessage `json:"-"`
	CallsPerMinute   int                        `json:"calls_per_minute"`
	QueueCapacity    int                        `json:"queue_capacity"`
	CooldownMS       int                        `json:"cooldown_ms"`
	MaxParallelCalls int                        `json:"max_parallel_calls"`
	EndToEndMS       int                        `json:"end_to_end_ms"`
}

// AudioClip is ephemeral immutable media. PCM16 is kept in RAM and converted to
// the transport-specific container only by the adapter that needs it.
type AudioClip struct {
	PCM16      []int16   `json:"-"`
	DurationMS int       `json:"duration_ms"`
	SampleRate int       `json:"sample_rate"`
	Channels   int       `json:"channels"`
	StartedAt  time.Time `json:"started_at,omitempty"`
	EndedAt    time.Time `json:"ended_at,omitempty"`
	SegmentID  string    `json:"segment_id,omitempty"`
	Final      bool      `json:"final"`
}

const (
	OutcomeNoAction    Outcome = "no_action"
	OutcomeRespond     Outcome = "respond"
	OutcomeNeedScreen  Outcome = "need_screen"
	OutcomeContextOnly Outcome = "context_only"
	// OutcomeWait is accepted while migrating the unfinished schema-4 branch.
	OutcomeWait Outcome = "wait"
)

func IsErrorOutcome(o Outcome) bool {
	return o != OutcomeSuccess && o != OutcomeNoAction && o != OutcomeRespond && o != OutcomeNeedScreen && o != OutcomeContextOnly && o != OutcomeWait
}

func (m ModelMeta) HasAudioInput() bool {
	for _, s := range m.InputModalities {
		if s == "audio" {
			return true
		}
	}
	return false
}
