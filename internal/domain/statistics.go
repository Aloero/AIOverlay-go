package domain

import "encoding/json"

// RuntimeStatistics is opt-in, contains no prompts, images or credentials, and
// never replaces manually assigned model quality scores.
type RuntimeStatistics struct {
	Extra   map[string]json.RawMessage `json:"-"`
	Models  map[string]ModelStatistics `json:"models"`
	Cursors map[string]int             `json:"cursors"`
}
type ModelStatistics struct {
	Extra       map[string]json.RawMessage `json:"-"`
	Success     int                        `json:"success"`
	Failure     int                        `json:"failure"`
	TTFTSamples int                        `json:"ttft_samples"`
	AvgTTFT     float64                    `json:"avg_ttft_ms"`
	AvgDuration float64                    `json:"avg_duration_ms"`
}
