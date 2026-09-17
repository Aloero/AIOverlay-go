package domain

import "encoding/json"

// EventSource binds one independently scheduled source to a graph entry.
// Absent sources preserve the legacy manual screenshot entry exactly.
type EventSource struct {
	Extra       map[string]json.RawMessage `json:"-"`
	ID          string                     `json:"id"`
	Name        string                     `json:"name"`
	Kind        string                     `json:"kind"` // manual_screen, audio, screen_timer
	Enabled     bool                       `json:"enabled"`
	Command     string                     `json:"command,omitempty"`
	EntryNodeID string                     `json:"entry_node_id"`
	Intent      string                     `json:"intent"` // respond, decide, context
	IntervalMS  int                        `json:"interval_ms,omitempty"`
	ChangedOnly bool                       `json:"changed_only,omitempty"`
	Audio       *AudioSourceConfig         `json:"audio,omitempty"`
}
type AudioSourceConfig struct {
	Extra     map[string]json.RawMessage `json:"-"`
	Mode      string                     `json:"mode"`                // system (WASAPI render loopback), microphone
	DeviceID  string                     `json:"device_id,omitempty"` // empty = current default, no fallback for explicit IDs
	SegmentMS int                        `json:"segment_ms"`
	SilenceMS int                        `json:"silence_ms"`
	MinimumMS int                        `json:"minimum_ms"`
	Threshold float64                    `json:"threshold"` // RMS amplitude, not semantic speech detection
}
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
	Extra          map[string]json.RawMessage `json:"-"`
	CallsPerMinute int                        `json:"calls_per_minute"`
	QueueCapacity  int                        `json:"queue_capacity"`
	CooldownMS     int                        `json:"cooldown_ms"`
}

// AudioClip is an ephemeral immutable WAV payload; never put it in configuration.
type AudioClip struct {
	WAV        []byte `json:"-"`
	DurationMS int    `json:"duration_ms"`
	SampleRate int    `json:"sample_rate"`
	Channels   int    `json:"channels"`
}

const (
	OutcomeWait       Outcome = "wait"
	OutcomeRespond    Outcome = "respond"
	OutcomeNeedScreen Outcome = "need_screen"
)

func IsErrorOutcome(o Outcome) bool {
	return o != OutcomeSuccess && o != OutcomeWait && o != OutcomeRespond && o != OutcomeNeedScreen
}

func (m ModelMeta) HasAudioInput() bool {
	for _, s := range m.InputModalities {
		if s == "audio" {
			return true
		}
	}
	return false
}
